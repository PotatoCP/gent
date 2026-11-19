package compaction_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/agents/react"
	"github.com/rickchristie/gent/compaction"
	"github.com/rickchristie/gent/executor"
	"github.com/rickchristie/gent/format"
	"github.com/rickchristie/gent/internal/tt"
	"github.com/rickchristie/gent/toolchain"
	"github.com/stretchr/testify/assert"
)

func TestSummarization_NativeToolEvidence(t *testing.T) {
	type input struct{ content string }
	type expected struct {
		prompt string
		err    error
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"calls without assistant text", input{}, expected{`## Existing Summary

None (first compaction).
### Message 1

Role: human
Read both records.

### Message 2

Role: ai
Assistant item: intermediary

Tool call "call-a": lookup
Arguments: {"id":7}
Tool call "call-b": lookup
Arguments: {"id":8}
Role: tool
Tool result "call-a": lookup
record 7
Role: tool
Tool result "call-b": lookup
record 8

`, nil}},
		{"calls with assistant text", input{"I will read both records."},
			expected{`## Existing Summary

None (first compaction).
### Message 1

Role: human
Read both records.

### Message 2

Role: ai
Assistant item: intermediary
I will read both records.
Tool call "call-a": lookup
Arguments: {"id":7}
Tool call "call-b": lookup
Arguments: {"id":8}
Role: tool
Tool result "call-a": lookup
record 7
Role: tool
Tool result "call-b": lookup
record 8

`, nil}},
	}
	for _, test := range cases {
		t.Run(
			test.name,
			func(t *testing.T) {
				model := tt.NewMockModel().AddRawResponse(
					&gent.ModelCallResponse{

						Choices: []*gent.ContentChoice{
							{
								Completion: gent.ModelCompletion{

									Status: gent.ModelStatusToolCalls,
								},
								Parts: append(
									[]gent.ContentPart{
										&gent.AssistantContent{
											Type:      gent.AssistantContentIntermediary,
											MessageId: "answer",
											Content: []gent.ContentPart{

												gent.TextContent{Text: test.input.content},
											},
										},
									},
									[]gent.ContentPart{
										&gent.ModelToolCallRequest{
											ID:   "call-a",
											Type: "function",
											FunctionCall: &gent.FunctionCall{

												Name:      "lookup",
												Arguments: `{"id":7}`,
											},
										},
										&gent.ModelToolCallRequest{
											ID:   "call-b",
											Type: "function",
											FunctionCall: &gent.FunctionCall{

												Name:      "lookup",
												Arguments: `{"id":8}`,
											},
										},
									}...,
								),
							},
						},
					},
				)
				chain := toolchain.New().RegisterTool(gent.DefineTool("lookup",
					func(_ context.Context, args map[string]any) (*gent.ToolExecutionResponse, error) {
						return capturedToolResponse(fmt.Sprintf("record %v", args["id"]), nil)
					}).Description("Read").ParameterSchema(nil).MustBuild())
				var originals []*gent.HistoryEntry
				codecs := gent.NewCodecRegistry()
				recorder := &nativeSummaryRecorder{codecs: codecs}
				loop := &nativeSummaryLoop{Agent: react.NewAgent(model)}
				loop.check = func(execCtx *gent.ExecutionContext, result *gent.AgentLoopResult) {
					assert.Equal(t, &gent.AgentLoopResult{Action: gent.LAContinue}, result)
					original, err := execCtx.Scratchpad()
					if !assert.NoError(t, err) {
						return
					}
					summary := tt.NewMockModel().AddResponse("Read records 7 and 8.", 0, 0)
					strategy := compaction.NewSummarization(summary, format.NewXML()).WithPrompt(`%s
%s`)
					selection, err := execCtx.CompactionInput()
					if !assert.NoError(t, err) {
						return
					}
					update, err := strategy.Compact(execCtx, selection)
					if !assert.Equal(t, test.expected.err, err) {
						return
					}
					assert.Equal(t, [][]*gent.ModelMessage{{gent.TextParts(gent.ChatMessageTypeHuman,
						test.expected.prompt)}}, summary.CapturedMessages)
					assert.NoError(t, execCtx.UpdateHistory(*update))
					active, err := execCtx.Scratchpad()
					if !assert.NoError(t, err) || !assert.Len(t, active, 2) {
						return
					}
					assert.Equal(t, update.Active, execCtx.ContextDraft().Active)
					assert.Equal(t, original[0].Messages, active[0].Messages)
					assert.Equal(t, []*gent.MessageContent{{Role: gent.ChatMessageTypeAI,
						Parts: []gent.ContentPart{gent.TextContent{Text: `<prior_work_summary>
Read records 7 and 8.
</prior_work_summary>`}}}}, active[1].Messages)
					originals = original
				}
				runner := executor.NewRunner(loop, &gent.Config{
					ToolChain: chain, Codecs: codecs, Recorder: recorder,
				})
				execCtx, err := runner.NewExecutionContext(context.Background(), t.Name())
				if !assert.NoError(t, err) {
					return
				}
				t.Cleanup(execCtx.Release)
				answer, _, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Read both records.")))
				assert.NoError(t, err)
				assert.Equal(t, "checked", answer)
				// Full archive replay owns the originals after compact execution state prunes them.
				for _, entry := range originals {
					saved, found := recorder.view.History.Entry(entry.Id)
					assert.True(t, found)
					assert.Equal(t, entry, saved)
				}
			},
		)
	}
}

func TestSummarization_TextAndMedia(t *testing.T) {
	execCtx := tt.NewContext(t, &gent.Config{})
	assert.NoError(t, execCtx.AppendRecord(&gent.HistoryEntry{Messages: []*gent.MessageContent{
		{Role: gent.ChatMessageTypeHuman, Parts: []gent.ContentPart{
			gent.TextContent{Text: "Read the image."},
			gent.ImageURLContent{URL: "https://example.com/image.png"},
		}},
		{Role: gent.ChatMessageTypeAI, Parts: []gent.ContentPart{
			gent.TextContent{Text: "The image has two records."},
		}},
	}}, true))
	model := tt.NewMockModel().AddResponse("Two records.", 0, 0)
	strategy := compaction.NewSummarization(model, format.NewXML()).WithPrompt(`%s
%s`)
	selection, err := execCtx.CompactionInput()
	if !assert.NoError(t, err) {
		return
	}
	_, err = strategy.Compact(execCtx, selection)
	assert.NoError(t, err)
	assert.Equal(t, [][]*gent.ModelMessage{{gent.TextParts(gent.ChatMessageTypeHuman,
		`## Existing Summary

None (first compaction).
### Message 1

Role: human
Read the image.
Role: ai
The image has two records.

`), &gent.ModelMessage{Role: gent.ChatMessageTypeHuman, Parts: []gent.ContentPart{
		gent.TextPart(fmt.Sprintf("Attached context from entry %q, message 0, role human:",
			selection.Entries[0].Id)), gent.ImageURLPart("https://example.com/image.png"),
	}}}}, model.CapturedMessages)
}

// The real ReAct iteration runs inside an admitted turn so native tools cross normal saves.
// The check inspects compaction before the fixture supplies its accepted text result.
type nativeSummaryLoop struct {
	*react.Agent[string]
	check func(*gent.ExecutionContext, *gent.AgentLoopResult)
}

func (loop *nativeSummaryLoop) Next(execCtx *gent.ExecutionContext) (*gent.AgentLoopResult, error) {
	result, err := loop.Agent.Next(execCtx)
	if err != nil {
		return nil, err
	}
	loop.check(execCtx, result)
	return gent.AcceptOutput(execCtx, &gent.OutputResult[string]{Accepted: true, Value: "checked",
		Content: []gent.ContentPart{gent.TextPart("checked")}}, nil)
}

// Encode and decode each read-only batch before archive replay so the view owns its input.
type nativeSummaryRecorder struct {
	codecs *gent.CodecRegistry
	view   *gent.RunView
}

func (recorder *nativeSummaryRecorder) Save(_ context.Context, batch *gent.SaveBatch,
	_ func() (*gent.RunState, error),
) error {
	data, err := recorder.codecs.Marshal(batch)
	if err != nil {
		return err
	}
	var owned gent.SaveBatch
	if err := recorder.codecs.Unmarshal(data, &owned); err != nil {
		return err
	}
	if recorder.view == nil {
		recorder.view = gent.NewRunView(batch.RunId)
	}
	result, err := recorder.view.Apply(&owned, recorder.codecs)
	if err != nil {
		return err
	}
	if result.Status != gent.ApplyApplied {
		return fmt.Errorf("archive apply: %s: %s", result.Status, result.Detail)
	}
	return nil
}
