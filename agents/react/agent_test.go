package react

import (
	"context"
	"errors"
	"fmt"
	"github.com/rickchristie/gent/executor"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/format"
	"github.com/rickchristie/gent/toolchain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ----------------------------------------------------------------------------
// Mock Model for testing
// ----------------------------------------------------------------------------

type mockModel struct {
	responses        []*gent.ModelCallResponse
	errors           []error
	callCount        int
	capturedOptions  [][]gent.CallOption
	capturedMessages [][]*gent.ModelMessage
}

func newMockModel(responses ...*gent.ModelCallResponse) *mockModel {
	return &mockModel{responses: responses}
}

func (m *mockModel) WithErrors(errs ...error) *mockModel {
	m.errors = errs
	return m
}

func (m *mockModel) response(request *gent.ModelCallRequest) (*gent.ModelCallResponse, error) {
	m.capturedMessages = append(m.capturedMessages, request.Messages)
	options := []gent.CallOption{gent.WithOptions(&request.Options)}
	m.capturedOptions = append(m.capturedOptions, options)
	idx := m.callCount
	m.callCount++

	if idx < len(m.errors) && m.errors[idx] != nil {
		return nil, m.errors[idx]
	}

	if idx < len(m.responses) {
		return m.responses[idx], nil
	}

	return &gent.ModelCallResponse{
		Choices: []*gent.ContentChoice{
			answerChoice(""),
		},
	}, nil
}

func (m *mockModel) GenerateContentStream(
	_ context.Context, request *gent.ModelCallRequest,
) (gent.Stream, error) {
	resp, err := m.response(request)
	if err != nil {
		return gent.NewCompletedStream(nil, err), nil
	}
	return gent.NewCompletedStream(resp, nil), nil
}

// ----------------------------------------------------------------------------
// Mock ToolChain for testing
// ----------------------------------------------------------------------------

func newMockToolChain() *toolchain.Registry { return toolchain.New() }

// ----------------------------------------------------------------------------
// Helper to create ExecutionContext for tests
// ----------------------------------------------------------------------------

func answerChoice(text string) *gent.ContentChoice {
	return &gent.ContentChoice{
		Completion:      gent.ModelCompletion{Status: gent.ModelStatusAnswer},
		CandidateSource: &gent.ContentSource{MessageIds: []string{"answer"}},
		Parts: []gent.ContentPart{&gent.AssistantContent{
			Type: gent.AssistantContentFinalAnswerCandidate, MessageId: "answer",
			Content: []gent.ContentPart{gent.TextPart(text)},
		}},
	}
}

func newTestExecution[T any](t *testing.T, loop gent.Loop[T], config *gent.Config) (
	*executor.Runner[T], *gent.ExecutionContext,
) {
	t.Helper()
	if recorder, ok := config.Recorder.(*testRecorder); ok {
		if config.Codecs == nil {
			config.Codecs = gent.NewCodecRegistry()
		}
		recorder.codecs = config.Codecs
	}
	runner := executor.NewRunner(loop, config)
	execCtx, err := runner.NewExecutionContext(context.Background(), t.Name())
	require.NoError(t, err)
	t.Cleanup(execCtx.Release)
	return runner, execCtx
}

// testRecorder retains independent saved values so tests can inspect the full accepted archive.
// The execution context keeps only the active working state.
type testRecorder struct {
	codecs  *gent.CodecRegistry
	view    *gent.RunView
	state   *gent.RunState
	batches []*gent.SaveBatch
}

func (recorder *testRecorder) Save(_ context.Context, batch *gent.SaveBatch,
	capture func() (*gent.RunState, error),
) error {
	if recorder.codecs == nil {
		recorder.codecs = gent.NewCodecRegistry()
	}
	data, err := recorder.codecs.Marshal(batch)
	if err != nil {
		return err
	}
	var saved gent.SaveBatch
	if err := recorder.codecs.Unmarshal(data, &saved); err != nil {
		return err
	}
	if recorder.view == nil {
		recorder.view = gent.NewRunView(batch.RunId)
	}
	applied, err := recorder.view.Apply(&saved, recorder.codecs)
	if err != nil {
		return err
	}
	if applied.Status != gent.ApplyApplied {
		return fmt.Errorf("save batch: %s", applied.Status)
	}
	recorder.batches = append(recorder.batches, &saved)
	recorder.state, err = capture()
	return err
}

func savedAssistantId(t *testing.T, recorder *testRecorder, call, part int) string {
	t.Helper()
	index := 0
	for _, event := range recorder.view.Activity {
		response, ok := event.Payload.(*gent.AfterModelCallEvent)
		if !ok {
			continue
		}
		if index == call {
			return response.Response.Choices[0].Parts[part].(*gent.AssistantContent).MessageId
		}
		index++
	}
	t.Fatal("saved model response is absent")
	return ""
}

// ----------------------------------------------------------------------------
// Agent Tests
// ----------------------------------------------------------------------------

func TestAgent_Next_ModelError(t *testing.T) {
	model := newMockModel().WithErrors(errors.New("model failed"))
	loop := NewAgent(model).WithToolResultFormat(format.NewXML())
	runner, execCtx := newTestExecution(t, loop, &gent.Config{})
	value, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Hello")))
	require.Error(t, err)
	assert.Equal(t, "", value)
	assert.Nil(t, result)
	assert.ErrorIs(t, err, model.errors[0])
	assert.EqualError(t, err, fmt.Sprintf(
		"gent: Loop.Next %T failed in run %q turn %q iteration 1: model call failed: model failed",
		loop, execCtx.RunId(), execCtx.TurnId()))

}

// mockNamerModel implements Model with ModelNamer for
// testing model-specific sampling behavior.
type mockNamerModel struct {
	mockModel
	name string
}

func (m *mockNamerModel) ModelName() string {
	return m.name
}

func TestAgent_WithCallOptions(t *testing.T) {
	// The ReAct agent prepends DefaultSamplingParams before
	// user options. mockModel does not implement ModelNamer,
	// so defaults are temp=0.2, top-p=1.0. User options
	// applied after override the defaults.

	type input struct {
		callOptions []gent.CallOption
	}

	type expected struct {
		temperature float64
		topP        float64
		maxTokens   int
	}

	tests := []struct {
		name     string
		input    input
		expected expected
	}{
		{
			name: "no user options uses default " +
				"temperature and top-p",
			input: input{
				callOptions: nil,
			},
			expected: expected{
				temperature: 0.2,
				topP:        1.0,
			},
		},
		{
			name: "user temperature overrides " +
				"default",
			input: input{
				callOptions: []gent.CallOption{
					gent.WithTemperature(0.7),
				},
			},
			expected: expected{
				temperature: 0.7,
				topP:        1.0,
			},
		},
		{
			name: "user top-p overrides default",
			input: input{
				callOptions: []gent.CallOption{
					gent.WithTopP(0.9),
				},
			},
			expected: expected{
				temperature: 0.2,
				topP:        0.9,
			},
		},
		{
			name: "user overrides both temperature " +
				"and top-p",
			input: input{
				callOptions: []gent.CallOption{
					gent.WithTemperature(0.5),
					gent.WithTopP(0.8),
					gent.WithMaxTokens(512),
				},
			},
			expected: expected{
				temperature: 0.5,
				topP:        0.8,
				maxTokens:   512,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			model := newMockModel(
				&gent.ModelCallResponse{
					Choices: []*gent.ContentChoice{
						answerChoice("done"),
					},
				},
			)

			format := format.NewXML()

			agent := NewAgent(model).WithToolResultFormat(format)

			if tc.input.callOptions != nil {
				agent.WithCallOptions(
					tc.input.callOptions...,
				)
			}

			recorder := &testRecorder{}
			runner, execCtx := newTestExecution(t, agent, &gent.Config{Recorder: recorder})
			_, _, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("test")))
			require.NoError(t, err)
			require.Len(t, model.capturedOptions, 1)

			captured := model.capturedOptions[0]
			var opts gent.CallOptions
			for _, opt := range captured {
				opt(&opts)
			}
			assert.InDelta(t,
				tc.expected.temperature,
				samplingValue(opts.Temperature), 0.001,
				"temperature",
			)
			assert.InDelta(t,
				tc.expected.topP,
				samplingValue(opts.TopP), 0.001,
				"top-p",
			)
			if tc.expected.maxTokens > 0 {
				assert.Equal(t,
					tc.expected.maxTokens,
					opts.MaxTokens,
				)
			}
		})
	}
}

func TestAgent_SamplingParams_ForbiddenModel(
	t *testing.T,
) {
	// Reasoning models omit sampling parameters unless the user sets them.

	type input struct {
		modelName   string
		callOptions []gent.CallOption
	}

	type expected struct {
		temperature float64
		topP        float64
	}

	tests := []struct {
		name     string
		input    input
		expected expected
	}{
		{
			name: "reasoning model omits forbidden params",
			input: input{
				modelName:   "openai/o3",
				callOptions: nil,
			},
			expected: expected{
				temperature: 0,
				topP:        0,
			},
		},
		{
			name: "user can still force params " +
				"on forbidden model",
			input: input{
				modelName: "o4-mini",
				callOptions: []gent.CallOption{
					gent.WithTemperature(0.5),
				},
			},
			expected: expected{
				temperature: 0.5,
				topP:        0,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			inner := newMockModel(
				&gent.ModelCallResponse{
					Choices: []*gent.ContentChoice{
						answerChoice("done"),
					},
				},
			)
			model := &mockNamerModel{
				mockModel: *inner,
				name:      tc.input.modelName,
			}

			format := format.NewXML()

			agent := NewAgent(model).WithToolResultFormat(format)

			if tc.input.callOptions != nil {
				agent.WithCallOptions(
					tc.input.callOptions...,
				)
			}

			recorder := &testRecorder{}
			runner, execCtx := newTestExecution(t, agent, &gent.Config{Recorder: recorder})
			_, _, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("test")))
			require.NoError(t, err)
			require.Len(t, model.capturedOptions, 1)

			captured := model.capturedOptions[0]
			var opts gent.CallOptions
			for _, opt := range captured {
				opt(&opts)
			}
			assert.InDelta(t,
				tc.expected.temperature,
				samplingValue(opts.Temperature), 0.001,
				"temperature",
			)
			assert.InDelta(t,
				tc.expected.topP,
				samplingValue(opts.TopP), 0.001,
				"top-p",
			)
		})
	}
}

func TestAgent_SamplingWarnings(t *testing.T) {
	type input struct {
		modelName   string
		callOptions []gent.CallOption
	}

	type expected struct {
		restrictiveWarning bool
		forbiddenWarnings  int
	}

	tests := []struct {
		name     string
		input    input
		expected expected
	}{
		{
			name: "restrictive temp and top-p " +
				"emits warning",
			input: input{
				callOptions: []gent.CallOption{
					gent.WithTemperature(0.1),
					gent.WithTopP(0.5),
				},
			},
			expected: expected{
				restrictiveWarning: true,
			},
		},
		{
			name: "restrictive temp only does " +
				"not warn",
			input: input{
				callOptions: []gent.CallOption{
					gent.WithTemperature(0.1),
				},
			},
			expected: expected{
				restrictiveWarning: false,
			},
		},
		{
			name: "restrictive top-p with normal " +
				"temp does not warn",
			input: input{
				callOptions: []gent.CallOption{
					gent.WithTemperature(0.5),
					gent.WithTopP(0.5),
				},
			},
			expected: expected{
				restrictiveWarning: false,
			},
		},
		{
			name: "normal values do not warn",
			input: input{
				callOptions: nil,
			},
			expected: expected{
				restrictiveWarning: false,
			},
		},
		{
			name: "forbidden model with user " +
				"temp emits forbidden warning",
			input: input{
				modelName: "openai/o3",
				callOptions: []gent.CallOption{
					gent.WithTemperature(0.5),
				},
			},
			expected: expected{
				forbiddenWarnings: 1,
			},
		},
		{
			name: "forbidden model with user " +
				"temp and top-p emits two " +
				"forbidden warnings",
			input: input{
				modelName: "o4-mini",
				callOptions: []gent.CallOption{
					gent.WithTemperature(0.5),
					gent.WithTopP(0.9),
				},
			},
			expected: expected{
				forbiddenWarnings: 2,
			},
		},
		{
			name: "forbidden model without " +
				"user params emits no warnings",
			input: input{
				modelName:   "gpt-5",
				callOptions: nil,
			},
			expected: expected{
				forbiddenWarnings: 0,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var model gent.Model
			if tc.input.modelName != "" {
				inner := newMockModel(
					&gent.ModelCallResponse{
						Choices: []*gent.ContentChoice{
							answerChoice("ok"),
						},
					},
				)
				model = &mockNamerModel{
					mockModel: *inner,
					name:      tc.input.modelName,
				}
			} else {
				model = newMockModel(
					&gent.ModelCallResponse{
						Choices: []*gent.ContentChoice{
							answerChoice("ok"),
						},
					},
				)
			}

			format := format.NewXML()

			agent := NewAgent(model).WithToolResultFormat(format)

			if tc.input.callOptions != nil {
				agent.WithCallOptions(
					tc.input.callOptions...,
				)
			}

			recorder := &testRecorder{}
			runner, execCtx := newTestExecution(t, agent, &gent.Config{Recorder: recorder})
			_, _, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("test")))
			require.NoError(t, err)

			// Count warning events by name.
			var restrictive, forbidden int
			for _, evt := range recorder.view.Activity {
				ce, ok := evt.Payload.(*gent.CommonEvent)
				if !ok {
					continue
				}
				switch ce.EventName {
				case "gent:sampling_params_restrictive":
					restrictive++
				case "gent:sampling_param_forbidden":
					forbidden++
				}
			}

			assert.Equal(t,
				tc.expected.restrictiveWarning,
				restrictive > 0,
				"restrictive warning",
			)
			assert.Equal(t,
				tc.expected.forbiddenWarnings,
				forbidden,
				"forbidden warnings",
			)
		})
	}
}

func samplingValue(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}
func (m *mockModel) PrepareRequest(execCtx *gent.ExecutionContext,
	request *gent.ModelCallRequest, chain gent.ToolChain,
) (*gent.ModelCallRequest, error) {
	return gent.PrepareModelRequest(execCtx, request, chain)
}
