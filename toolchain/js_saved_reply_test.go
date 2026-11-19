package toolchain

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/executor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type programReplyStore struct {
	*toolTestRecorder
	accepted         *gent.ToolCallRecord
	runStateSnapshot []byte
	failure          error
}

func (store *programReplyStore) Save(ctx context.Context, batch *gent.SaveBatch,
	capture func() (*gent.RunState, error),
) error {
	if err := store.toolTestRecorder.Save(ctx, batch, capture); err != nil {
		return err
	}
	for _, event := range batch.Events {
		finished, ok := event.Payload.(*gent.AfterToolCallEvent)
		if !ok || finished.ToolName != nativeCodeTool {
			continue
		}
		store.accepted = finished.Output.(*gent.ToolCallRecord)
		state, err := capture()
		if err != nil {
			return err
		}
		store.runStateSnapshot, err = store.codecs.MarshalRunState(state)
		if err != nil {
			return err
		}
		// The after-tool save accepts the reply, plan cursor, and cleared program together.
		// Storage can commit it even when its acknowledgment does not reach the worker.
		return store.failure
	}
	return nil
}

func TestProgramReusesAcceptedOuterReply(t *testing.T) {
	type input struct{ saveError error }
	type expected struct{ calls, recoveries int }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"after tool before iteration completion", input{}, expected{1, 0}},
		{"lost acknowledgment", input{errors.New("outer reply acknowledgment lost")}, expected{1, 0}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			calls, recoveries := 0, 0
			build := func() *JsToolChainWrapper {
				tool := gent.DefineTool("fetch", func(context.Context, struct{}) (
					*gent.ToolExecutionResponse, error,
				) {
					calls++
					return &gent.ToolExecutionResponse{Status: gent.ToolExecuted,
						Parts: []gent.ContentPart{gent.PDFPart([]byte("%PDF-test"))}}, nil
				}).Recovery(func(context.Context, *gent.ToolRecoveryRequest[struct{}]) (
					*gent.ToolRecoveryResponse, error,
				) {
					recoveries++
					return &gent.ToolRecoveryResponse{Status: gent.ToolRecoveryUnresolved}, nil
				}).MustBuild()
				return NewJsToolChainWrapper(New().RegisterTool(tool))
			}
			chain := build()
			codecs := gent.NewCodecRegistry()
			store := &programReplyStore{toolTestRecorder: &toolTestRecorder{codecs: codecs},
				failure: test.input.saveError}
			raw, err := json.Marshal(codeInput{Code: `const reply=tool.call({tool:"fetch",args:{}});
tool.forward(reply.parts.filter(p=>p.kind==="bytes")); console.log("computed value");`})
			require.NoError(t, err)
			var plan *gent.ToolPlan
			loop := planTestLoop(chain,
				[]*gent.ModelToolCallRequest{nativeCall(nativeCodeTool, string(raw))}, &plan)
			runner := executor.NewRunner[string](loop, &gent.Config{Codecs: codecs,
				Recorder: store, ToolChain: chain})
			execCtx, err := runner.NewExecutionContext(context.Background(), "program")
			require.NoError(t, err)
			defer execCtx.Release()
			_, _, err = runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Fetch and display the PDF.")))
			assert.ErrorIs(t, err, test.input.saveError)
			require.NotNil(t, store.accepted)
			assert.Equal(t, []gent.ContentPart{
				gent.TextPart("Tool fetch (call " + execCtx.ContextId() + ":tool:2):"),
				gent.PDFPart([]byte("%PDF-test")), gent.TextPart(`<output>
computed value
</output>`),
			}, store.accepted.Response().Parts())
			// A fresh chain and registry must reuse the accepted outer reply. In particular,
			// rebuilding recovery output must not lose console output or forwarded bytes.
			freshChain, freshCodecs := build(), gent.NewCodecRegistry()
			freshStore := &toolTestRecorder{codecs: freshCodecs, capture: true}
			var resumed *gent.ToolPlan
			freshLoop := planTestLoop(freshChain, nil, &resumed)
			freshRunner, fresh := restoreToolTest(t, freshLoop, &gent.Config{Codecs: freshCodecs,
				Recorder: freshStore, ToolChain: freshChain}, store.runStateSnapshot)
			_, found, err := fresh.CheckpointState("gent:program")
			require.NoError(t, err)
			require.False(t, found)
			_, completed, err := freshRunner.Continue(fresh)
			require.NoError(t, err)
			require.Equal(t, gent.ExecutionFinished, completed.Status)
			assert.Equal(t, test.expected, expected{calls, recoveries})
			assert.Equal(t, []*gent.ToolCallRecord{store.accepted}, resumed.Results)
			assertProgramStateCleared(t, fresh)
			var toolEvents []string
			for _, data := range freshStore.batches {
				var batch gent.SaveBatch
				require.NoError(t, freshCodecs.Unmarshal(data, &batch))
				for _, event := range batch.Events {
					if event.ToolCallId != "" {
						toolEvents = append(toolEvents, event.Type)
					}
				}
			}
			assert.Nil(t, toolEvents)
		})
	}
}
