package gent_test

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/agents/react"
	"github.com/rickchristie/gent/executor"
	"github.com/rickchristie/gent/internal/tt"
	"github.com/stretchr/testify/assert"
)

type partialFailureModel struct {
	*tt.MockModel
	response *gent.ModelCallResponse
	chunks   []gent.StreamChunk
	failure  error
}

func (model *partialFailureModel) GenerateContentStream(context.Context, *gent.ModelCallRequest) (
	gent.Stream, error,
) {
	stream := gent.NewStreamWithDuration()
	for _, chunk := range model.chunks {
		stream.Send(chunk)
	}
	stream.Complete(model.response, model.failure)
	return stream, nil
}

// A provider failure and a local validation failure describe separate causes. The application
// needs both to diagnose partial output, and neither failure can accept the broken response.
func TestManagedStreamKeepsProviderAndValidationFailures(t *testing.T) {
	type input struct {
		response *gent.ModelCallResponse
		chunks   []gent.StreamChunk
		failure  error
	}
	type expected struct {
		localError    string
		providerCause bool
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	failure := errors.New("provider stream failed")
	partial := &gent.ModelCallResponse{Choices: []*gent.ContentChoice{{
		Completion: gent.ModelCompletion{Status: gent.ModelStatusIncomplete},
	}}}
	invalid := &gent.ModelCallResponse{Choices: []*gent.ContentChoice{{}}}
	completionError := `gent: model call {call} choice 0: gent: unknown model completion ""`
	cases := []testCase{
		{"no partial response", input{failure: failure}, expected{providerCause: true}},
		{"valid partial response", input{response: partial, failure: failure},
			expected{providerCause: true}},
		{"invalid partial response", input{response: invalid, failure: failure},
			expected{completionError, true}},
		{"validation failure without provider failure", input{response: invalid},
			expected{localError: completionError}},
		{"invalid progress", input{response: partial, failure: failure,
			chunks: []gent.StreamChunk{{Content: []gent.ContentPart{gent.TextPart("partial")}}}},
			expected{"gent: assistant response requires a group or tool call, got gent.TextContent", true}},
		{"invalid progress source", input{response: partial, failure: failure,
			chunks: []gent.StreamChunk{{SourceMessageIds: []string{"unaccepted"}}}},
			expected{"gent: provider progress cannot publish accepted source links", true}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			model := &partialFailureModel{MockModel: tt.NewMockModel(),
				response: test.input.response, chunks: test.input.chunks, failure: test.input.failure}
			loop := react.NewAgent(model)
			recorder := &chainFailureRecorder{checkpointStore: newCheckpointStore()}
			var callId string
			runner := executor.NewRunner(loop, &gent.Config{
				Recorder: recorder, Codecs: recorder.codecs,
				Hooks: &gent.Hooks{BeforeModelCall: func(_ context.Context,
					event *gent.BeforeModelCallEvent,
				) error {
					callId = event.ModelCallId
					return nil
				}},
			})
			execCtx, err := runner.NewExecutionContext(context.Background(), test.name)
			if !assert.NoError(t, err) {
				return
			}
			defer execCtx.Release()
			value, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Read.")))
			if !assert.Error(t, err) {
				return
			}
			validation := strings.ReplaceAll(test.expected.localError, "{call}", strconv.Quote(callId))
			detail := validation
			if test.input.failure != nil {
				detail = test.input.failure.Error()
				if validation != "" {
					detail = fmt.Sprintf(`%s
%s`, detail, validation)
				}
			}
			if validation != "" {
				detail = fmt.Sprintf(
					"gent: model stream/response for call %q in run %q turn %q context %q: %s",
					callId, execCtx.RunId(), execCtx.TurnId(), execCtx.ContextId(), detail)
			}
			detail = fmt.Sprintf("model call failed: %s", detail)
			message := fmt.Sprintf("gent: Loop.Next %T failed in run %q turn %q iteration 1: %s",
				loop, execCtx.RunId(), execCtx.TurnId(), detail)
			type outcome struct {
				value         string
				result        *gent.ExecutionResult
				providerCause bool
				errorMessage  string
				phases        []gent.ExecutionPhase
			}
			assert.Equal(t, outcome{providerCause: test.expected.providerCause, errorMessage: message,
				phases: []gent.ExecutionPhase{gent.PhaseInitial, gent.PhaseIterationReady}},
				outcome{value, result, errors.Is(err, failure), err.Error(), recorder.phases})
			assertChainFailureRecovery(t, recorder, func() *executor.Runner[string] {
				return executor.NewRunner(react.NewAgent(tt.NewMockModel().
					AddResponse("Recovered", 0, 0)), &gent.Config{Codecs: recorder.codecs})
			})
		})
	}
}
