package react

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/rickchristie/gent"
	agentutil "github.com/rickchristie/gent/agents"
	"github.com/rickchristie/gent/executor"
	"github.com/rickchristie/gent/output"
	"github.com/rickchristie/gent/toolchain"
	"github.com/stretchr/testify/assert"
)

type guardedModel struct {
	choice        *gent.ContentChoice
	streamed      bool
	completionErr error
	calls         int
}

const guardedText = "abcdabcdabcdabcd"

func (m *guardedModel) GenerateContentStream(ctx context.Context, _ *gent.ModelCallRequest) (
	gent.Stream, error,
) {
	m.calls++
	if m.calls > 1 {
		return gent.NewCompletedStream(
			&gent.ModelCallResponse{
				Choices: []*gent.ContentChoice{
					answerChoice(
						"OK",
					),
				},
			},
			nil,
		), nil
	}
	stream := gent.NewStreamWithDuration()
	response := &gent.ModelCallResponse{Choices: []*gent.ContentChoice{m.choice}}
	if !m.streamed {
		stream.Complete(response, m.completionErr)
		return stream, nil
	}
	go func() {
		stream.Send(gent.StreamChunk{Content: []gent.ContentPart{&gent.AssistantContent{
			Type: gent.AssistantContentIntermediary, MessageId: "guard",
			Content: []gent.ContentPart{gent.TextPart(guardedText)},
		}}})
		// The guard must close the producer, then use its completion and cleanup result.
		<-ctx.Done()
		stream.Complete(response, m.completionErr)
	}()
	return stream, nil
}

func TestRefusalBeforeResponseGuards(t *testing.T) {
	type input struct{ streamed, tools, repetition bool }
	type expected struct {
		result *gent.ExecutionResult
		calls  int
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	refusal := &gent.ModelRefusal{Code: "content_filter", Content: []gent.ContentPart{
		gent.TextContent{Text: "The provider declined this request."}}}
	want := expected{
		&gent.ExecutionResult{
			Status:            gent.ExecutionFinished,
			TerminationReason: gent.TerminationRefused,
			Refusal:           refusal,
		},
		1,
	}
	cases := []testCase{
		{"final text size", input{}, want},
		{"final calls size", input{tools: true}, want},
		{"stream text size", input{streamed: true}, want},
		{"stream calls size", input{streamed: true, tools: true}, want},
		{"final text repetition", input{repetition: true}, want},
		{"final calls repetition", input{tools: true, repetition: true}, want},
		{"stream text repetition", input{streamed: true, repetition: true}, want},
		{"stream calls repetition", input{streamed: true, tools: true, repetition: true}, want},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			choice := &gent.ContentChoice{Completion: gent.ModelCompletion{
				Status: gent.ModelStatusRefused, Refusal: refusal}}
			if test.input.tools {
				choice.Parts = []gent.ContentPart{nativeTestCall("call", "effect", guardedText)}
			} else {
				choice.Parts = []gent.ContentPart{&gent.AssistantContent{
					Type: gent.AssistantContentIntermediary, MessageId: "guard",
					Content: []gent.ContentPart{gent.TextPart(guardedText)},
				}}
			}
			model := &guardedModel{choice: choice, streamed: test.input.streamed}
			validations, toolCalls := 0, 0
			out := output.NewTextOutput().SetCustomValidator("check", func(
				*gent.ExecutionContext, string,
			) (*gent.OutputValidationResult, error) {
				validations++
				return &gent.OutputValidationResult{Accepted: true}, nil
			})
			loop := NewWithOutput(model, out).WithMaxResponseChars(3)
			config := agentutil.DefaultRepetitionConfig()
			config.Enabled, config.BlockSize = test.input.repetition, 4
			config.Action = agentutil.RepetitionTerminate
			loop.WithRepetitionConfig(config)
			chain := toolchain.New().RegisterTool(gent.DefineTool("effect",
				func(context.Context, map[string]any) (*gent.ToolExecutionResponse, error) {
					toolCalls++
					return capturedToolResponse("done", nil)
				}).Description("Perform an operation").ParameterSchema(nil).MustBuild())
			if test.input.repetition {
				loop.WithMaxResponseChars(0)
			}
			base, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			codecs := gent.NewCodecRegistry()
			recorder := &testRecorder{codecs: codecs}
			runner := executor.NewRunner(loop, &gent.Config{
				ToolChain: chain, Recorder: recorder, Codecs: codecs,
			})
			execCtx, err := runner.NewExecutionContext(base, "guarded")
			if !assert.NoError(t, err) {
				return
			}
			defer execCtx.Release()
			value, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Read.")))
			assert.NoError(t, err)
			want := *test.expected.result
			want.TurnId = execCtx.TurnId()
			assert.Equal(t, expected{&want, test.expected.calls}, expected{result, model.calls})
			assert.Equal(t, "", value)
			assert.Equal(t, 0, validations)
			assert.Equal(t, 0, toolCalls)
			assert.Equal(t, int64(0), execCtx.Stats().GetCounter(gent.SCRepetitionLoopTotal))
			entries := execCtx.History().Entries
			if assert.Len(t, entries, 2) {
				parts := choice.Parts
				if !test.input.tools {
					item := *parts[0].(*gent.AssistantContent)
					item.MessageId = savedAssistantId(t, recorder, 0, 0)
					parts = []gent.ContentPart{&item}
				}
				assert.Equal(t, []*gent.MessageContent{{Role: gent.ChatMessageTypeAI,
					Parts: parts}}, entries[1].Messages)
			}
		})
	}
}

func TestResponseGuardCompletionErrors(t *testing.T) {
	type input struct {
		completionErr error
		refusal       bool
	}
	type expected struct {
		err   string
		calls int
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cleanupErr := errors.New("cleanup failed")
	cases := []testCase{
		{"cancel only", input{completionErr: context.Canceled}, expected{calls: 2}},
		{"wrapped cancel only", input{completionErr: fmt.Errorf("read: %w", context.Canceled)},
			expected{calls: 2}},
		{"cleanup error", input{completionErr: cleanupErr, refusal: true},
			expected{"cleanup failed", 1}},
		{"joined cleanup error", input{completionErr: errors.Join(context.Canceled, cleanupErr),
			refusal: true}, expected{`context canceled
cleanup failed`, 1}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			choice := &gent.ContentChoice{Completion: gent.ModelCompletion{
				Status: gent.ModelStatusIncomplete}}
			if test.input.refusal {
				choice.Completion = gent.ModelCompletion{Status: gent.ModelStatusRefused,
					Refusal: &gent.ModelRefusal{Code: "blocked"}}
			}
			model := &guardedModel{choice: choice, streamed: true, completionErr: test.input.completionErr}
			loop := NewAgent(model).WithMaxResponseChars(3).
				WithRepetitionConfig(agentutil.RepetitionConfig{})
			base, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			runner := executor.NewRunner(loop, &gent.Config{})
			execCtx, err := runner.NewExecutionContext(base, "guarded")
			if !assert.NoError(t, err) {
				return
			}
			defer execCtx.Release()
			value, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Read.")))
			got := expected{calls: model.calls}
			if err != nil {
				got.err = err.Error()
			}
			want := test.expected
			if want.err != "" {
				want.err = fmt.Sprintf("gent: Loop.Next %T failed in run %q turn %q iteration 1: "+
					"model call failed: %s", loop, execCtx.RunId(), execCtx.TurnId(), want.err)
			}
			assert.Equal(t, want, got)
			if test.expected.err == "" {
				assert.Equal(t, "OK", value)
				assert.Equal(t, &gent.ExecutionResult{Status: gent.ExecutionFinished, TurnId: execCtx.TurnId(),
					TerminationReason: gent.TerminationSuccess,
					Output:            []gent.ContentPart{gent.TextContent{Text: "OK"}}}, result)
				return
			}
			assert.Equal(t, "", value)
			assert.ErrorIs(t, err, cleanupErr)
			assert.Nil(t, result)
			if assert.Len(t, execCtx.History().Entries, 1) {
				assert.Equal(t, []*gent.MessageContent{{Role: gent.ChatMessageTypeHuman,
					Parts: []gent.ContentPart{gent.TextPart("Read.")}}},
					execCtx.History().Entries[0].Messages)
			}
		})
	}
}
func (m *guardedModel) PrepareRequest(execCtx *gent.ExecutionContext,
	request *gent.ModelCallRequest, chain gent.ToolChain,
) (*gent.ModelCallRequest, error) {
	return gent.PrepareModelRequest(execCtx, request, chain)
}
