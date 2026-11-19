package react

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rickchristie/gent"
	agentutil "github.com/rickchristie/gent/agents"
	"github.com/rickchristie/gent/output"
	"github.com/rickchristie/gent/toolchain"
	"github.com/stretchr/testify/assert"
)

func nativeTestCall(id, name, args string) *gent.ModelToolCallRequest {
	return &gent.ModelToolCallRequest{ID: id, Type: "function",
		FunctionCall: &gent.FunctionCall{Name: name, Arguments: args}}
}

func nativeTestResponse(choice *gent.ContentChoice) *gent.ModelCallResponse {
	if choice == nil {
		return &gent.ModelCallResponse{}
	}
	return &gent.ModelCallResponse{Choices: []*gent.ContentChoice{choice}}
}

// This adapter has no display chunks. Its completed response still needs the same size and
// repetition checks as a streamed response, including arguments that arrive only on completion.
type nativeFinalModel struct{ *mockModel }

func (model *nativeFinalModel) GenerateContentStream(_ context.Context,
	request *gent.ModelCallRequest) (gent.Stream, error) {
	response, err := model.response(request)
	stream := gent.NewStreamWithDuration()
	stream.Complete(response, err)
	return stream, nil
}

type nativeMediaTool struct {
	gent.Tool[map[string]any]
}

func (tool *nativeMediaTool) Call(ctx context.Context, args map[string]any) (
	*gent.ToolExecutionResponse, error,
) {
	result, err := tool.Tool.Call(ctx, args)
	if err != nil {
		return nil, err
	}
	result.Instructions = "Use the image."
	result.Parts = append(result.Parts, gent.ImageURLPart("https://example.com/record.png"))
	return result, nil
}

func TestNativeAgent_RequestAndToolHistory(t *testing.T) {
	var inputs []map[string]any
	tool := &nativeMediaTool{gent.DefineTool("lookup",
		func(_ context.Context, args map[string]any) (*gent.ToolExecutionResponse, error) {
			inputs = append(inputs, args)
			return capturedToolResponse("record", nil)
		}).Description("Read a record").ParameterSchema(nil).MustBuild()}
	calls := []*gent.ModelToolCallRequest{nativeTestCall("call-a", "lookup", `{"id":7}`),
		nativeTestCall("call-b", "lookup", `{"id":8}`)}
	parts := []gent.ContentPart{
		&gent.AssistantContent{Type: gent.AssistantContentReasoning, MessageId: "reasoning",
			Content: []gent.ContentPart{gent.TextPart("private reasoning")}},
		&gent.AssistantContent{Type: gent.AssistantContentIntermediary, MessageId: "commentary",
			Content: []gent.ContentPart{gent.TextPart("I will check.")}},
		calls[0], calls[1],
	}
	model := newMockModel(nativeTestResponse(&gent.ContentChoice{
		Completion: gent.ModelCompletion{Status: gent.ModelStatusToolCalls}, Parts: parts,
	}), nativeTestResponse(answerChoice("Done.")))
	agent := NewAgent(model).WithCallOptions(gent.WithTemperature(0.4), gent.WithTopP(0.9))
	recorder := &testRecorder{}
	runner, execCtx := newTestExecution(t, agent, &gent.Config{
		ToolChain: toolchain.New().RegisterTool(tool), Recorder: recorder,
	})
	system := "Support agent. Read only."
	value, result, err := runner.NewTurn(
		execCtx,
		&gent.Task{

			Instructions: []*gent.ModelMessage{gent.TextParts(gent.ChatMessageTypeSystem, system)},
			Messages: []*gent.ModelMessage{
				gent.TextParts(
					gent.ChatMessageTypeHuman,
					"Read two records.",
				),
			},
		},
	)
	if !assert.NoError(t, err) {
		return
	}
	assert.Equal(t, "Done.", value)
	assert.Equal(t, &gent.ExecutionResult{Status: gent.ExecutionFinished, TurnId: execCtx.TurnId(),
		TerminationReason: gent.TerminationSuccess,
		Output:            []gent.ContentPart{gent.TextPart("Done.")}}, result)
	makeReply := func(id string) *gent.ModelToolCallResponse {
		parts := []gent.ContentPart{gent.TextPart("record"),
			gent.ImageURLPart("https://example.com/record.png"), gent.TextPart("Use the image.")}
		reply, err := preparedTestReply(id, "lookup", "record").WithParts(parts...)
		assert.NoError(t, err)
		return reply
	}

	initial := []*gent.ModelMessage{gent.TextParts(gent.ChatMessageTypeSystem, system),
		gent.TextParts(gent.ChatMessageTypeHuman, "Read two records.")}
	reasoning := *parts[0].(*gent.AssistantContent)
	commentary := *parts[1].(*gent.AssistantContent)
	reasoning.MessageId = savedAssistantId(t, recorder, 0, 0)
	commentary.MessageId = savedAssistantId(t, recorder, 0, 1)
	want := append(append([]*gent.ModelMessage(nil), initial...),
		&gent.ModelMessage{Role: gent.ChatMessageTypeAI,
			Parts: []gent.ContentPart{&reasoning, &commentary, calls[0], calls[1]}},
		&gent.ModelMessage{Role: gent.ChatMessageTypeTool, Parts: []gent.ContentPart{
			makeReply("call-a")}},
		&gent.ModelMessage{Role: gent.ChatMessageTypeTool, Parts: []gent.ContentPart{
			makeReply("call-b")}})
	assert.Equal(t, [][]*gent.ModelMessage{initial, want}, model.capturedMessages)
	definitions := []*gent.ToolDefinition{{Type: "function", Function: &gent.FunctionDefinition{
		Name: "lookup", Description: "Read a record",
		Parameters: map[string]any{"type": "object", "properties": map[string]any{}},
	}}}
	wantOptions := gent.NewModelCallRequest(nil, gent.WithTemperature(0.4),
		gent.WithTopP(0.9), gent.WithTools(definitions)).Options
	wantOptions.SteeringMessages = gent.DefaultSteeringMessages()
	wantOptions.ResponseMIMEType = "text/plain"
	wantOptions.ResponseModalities = []string{"text"}
	for _, options := range model.capturedOptions {
		assert.Equal(t, wantOptions, gent.NewModelCallRequest(nil, options...).Options)
	}
	assert.Equal(t, []map[string]any{{"id": json.Number("7")}, {"id": json.Number("8")}}, inputs)
	assert.Equal(t, int64(2), execCtx.Stats().GetCounter(gent.SCToolCalls))
	assert.NoError(t, gent.ValidateBundle(*recorder.view, execCtx.Codecs()))
}

func TestNativeAgent_RetryInvalidCalls(t *testing.T) {
	type input struct{ choice *gent.ContentChoice }
	type expected struct {
		feedback    string
		parseErrors int64
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"no choice", input{nil}, expected{"The model returned no response. Please try again.", 0}},
		{"incomplete",
			input{&gent.ContentChoice{Completion: gent.ModelCompletion{
				Status:         gent.ModelStatusIncomplete,
				ProviderReason: "length",
			}, Parts: []gent.ContentPart{nativeTestCall("a", "lookup", "{}")}}},
			expected{"The response was incomplete. Use a shorter answer or fewer tool calls.", 0}},
		{"empty",
			input{
				&gent.ContentChoice{Completion: gent.ModelCompletion{Status: gent.ModelStatusAnswer}},
			},
			expected{"The answer must contain text only. Return a text answer.", 0}},
		{"duplicate IDs",
			input{&gent.ContentChoice{Completion: gent.ModelCompletion{
				Status: gent.ModelStatusToolCalls,
			}, Parts: []gent.ContentPart{
				nativeTestCall("a", "lookup", "{}"), nativeTestCall("a", "lookup", "{}")}}},
			expected{`duplicate model tool call ID "a"`, 1}},
		{"bad JSON",
			input{&gent.ContentChoice{Completion: gent.ModelCompletion{
				Status: gent.ModelStatusToolCalls,
			}, Parts: []gent.ContentPart{nativeTestCall("a", "lookup", "{")}}},
			expected{gent.DefaultSteeringMessages().InvalidArguments + `
Invalid arguments for tool "lookup".
args: expected a complete JSON object with values compatible with the input schema.
Expected input schema:
{
  "type": "object"
}`, 0}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			model := newMockModel(nativeTestResponse(test.input.choice),
				nativeTestResponse(answerChoice("Done.")))
			chain := toolchain.New().RegisterTool(gent.DefineTool("lookup",
				func(context.Context, map[string]any) (*gent.ToolExecutionResponse, error) {
					t.Error("invalid response must not execute tools")
					return capturedToolResponse("", nil)
				}).Description("Read").ParameterSchema(nil).MustBuild())
			runner, execCtx := newTestExecution(t, NewAgent(model), &gent.Config{ToolChain: chain})
			value, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Read.")))
			if !assert.NoError(t, err) {
				return
			}
			assert.Equal(t, "Done.", value)
			assert.Equal(t, &gent.ExecutionResult{Status: gent.ExecutionFinished,
				TurnId: execCtx.TurnId(), TerminationReason: gent.TerminationSuccess,
				Output: []gent.ContentPart{gent.TextPart("Done.")}}, result)
			assert.Equal(t, test.expected.parseErrors,
				execCtx.Stats().GetCounter(gent.SCToolchainParseErrorTotal))
			if test.name == "bad JSON" {
				messages := model.capturedMessages[1]
				assert.Len(t, messages, 3)
				reply := messages[2].Parts[0].(*gent.ModelToolCallResponse)
				status, present := reply.ExecutionStatus()
				assert.True(t, present)
				assert.Equal(t, gent.ToolInvalidArguments, status)
				assert.Equal(t, test.expected.feedback, gent.ContentTextData(reply.Parts()))
				assert.Equal(t, int64(1), execCtx.Stats().GetCounter(gent.SCToolCallsInvalidArgumentsTotal))
				return
			}
			wantMessages := []*gent.ModelMessage{gent.TextParts(gent.ChatMessageTypeHuman, "Read.")}
			if test.input.choice != nil && test.input.choice.Completion.Status == gent.ModelStatusAnswer {
				wantMessages = append(wantMessages, &gent.ModelMessage{Role: gent.ChatMessageTypeAI})
			}
			wantMessages = append(wantMessages,
				gent.TextParts(gent.ChatMessageTypeHuman, test.expected.feedback))
			assert.Equal(t, wantMessages, model.capturedMessages[1])
			for _, message := range model.capturedMessages[1] {
				for _, part := range message.Parts {
					_, isCall := part.(*gent.ModelToolCallRequest)
					assert.False(t, isCall, "do not replay an invalid call group")
				}
			}
		})
	}
}

type nativeAnswer struct {
	Status string `json:"status"`
}

func TestNativeAgent_JSONValidation(t *testing.T) {
	model := newMockModel(nativeTestResponse(answerChoice("invalid")),
		nativeTestResponse(answerChoice(`{"status":"pending"}`)),
		nativeTestResponse(answerChoice(`{"status":"done"}`)))
	var values []nativeAnswer
	out, err := output.NewJsonOutput[nativeAnswer]()
	if !assert.NoError(t, err) {
		return
	}
	out.SetCustomValidator("status", func(_ *gent.ExecutionContext, value nativeAnswer) (
		*gent.OutputValidationResult, error,
	) {
		values = append(values, value)
		if value.Status == "done" {
			return &gent.OutputValidationResult{Accepted: true}, nil
		}
		return &gent.OutputValidationResult{Steering: []gent.ContentPart{
			gent.TextContent{Text: "Check the status."}}}, nil
	})
	agent := NewWithOutput(model, out)
	recorder := &testRecorder{}
	runner, execCtx := newTestExecution(t, agent, &gent.Config{Recorder: recorder})
	value, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Check.")))
	if !assert.NoError(t, err) {
		return
	}
	assert.Equal(t, nativeAnswer{Status: "done"}, value)
	assert.Equal(t, &gent.ExecutionResult{Status: gent.ExecutionFinished,
		TurnId: execCtx.TurnId(), TerminationReason: gent.TerminationSuccess,
		Output: []gent.ContentPart{gent.TextPart(`{"status":"done"}`)}}, result)
	thirdRequest := model.capturedMessages[2]
	pending := answerChoice(`{"status":"pending"}`)
	pending.Parts[0].(*gent.AssistantContent).MessageId = savedAssistantId(t, recorder, 1, 0)
	assert.Equal(t, []*gent.ModelMessage{
		&gent.ModelMessage{Role: gent.ChatMessageTypeAI, Parts: pending.Parts},
		gent.TextParts(gent.ChatMessageTypeHuman, "Check the status."),
	}, thirdRequest[len(thirdRequest)-2:])
	assert.Equal(t, []nativeAnswer{{"pending"}, {"done"}}, values)
	assert.Equal(t, int64(1), execCtx.Stats().GetCounter(gent.SCOutputLocalRejectedTotal))
	assert.Equal(t, int64(1), execCtx.Stats().GetCounter(gent.SCOutputCustomRejectedBy+"status"))
	assert.Equal(t, int64(2), execCtx.Stats().GetCounter(gent.SCOutputRejectedTotal))
	assert.Equal(t, float64(0), execCtx.Stats().GetGauge(gent.SGOutputLocalRejectedConsecutive))
	assert.Equal(t, float64(0), execCtx.Stats().GetGauge(gent.SGOutputCustomRejectedConsecutive))
	assert.Equal(t, float64(0), execCtx.Stats().GetGauge(gent.SGOutputRejectedConsecutive))
}

func TestNativeAgent_FinalOnlySizeAndRepetition(t *testing.T) {
	type input struct {
		content, args string
		max           int
		repetition    bool
	}
	type expected struct{ loops int64 }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"argument size", input{"", `{"id":"too much data"}`, 8, false}, expected{0}},
		{"text size", input{"too much text", "{}", 8, false}, expected{0}},
		{"repetition", input{strings.Repeat("abcdefghij", 40), "{}", 0, true}, expected{1}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			choice := answerChoice(test.input.content)
			choice.Completion.Status = gent.ModelStatusToolCalls
			choice.Parts = append(choice.Parts, nativeTestCall("a", "lookup", test.input.args))
			model := newMockModel(nativeTestResponse(choice), nativeTestResponse(answerChoice("Done.")))
			config := agentutil.DefaultRepetitionConfig()
			config.Enabled = test.input.repetition
			config.BlockSize = 50
			agent := NewAgent(&nativeFinalModel{model}).WithMaxResponseChars(test.input.max).
				WithRepetitionConfig(config)
			chain := toolchain.New().RegisterTool(gent.DefineTool("lookup",
				func(context.Context, map[string]any) (*gent.ToolExecutionResponse, error) {
					t.Error("truncated response must not execute tools")
					return capturedToolResponse("", nil)
				}).Description("Read").ParameterSchema(nil).MustBuild())
			runner, execCtx := newTestExecution(t, agent, &gent.Config{ToolChain: chain})
			value, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Read.")))
			if !assert.NoError(t, err) {
				return
			}
			assert.Equal(t, "Done.", value)
			assert.Equal(t, &gent.ExecutionResult{Status: gent.ExecutionFinished,
				TurnId: execCtx.TurnId(), TerminationReason: gent.TerminationSuccess,
				Output: []gent.ContentPart{gent.TextPart("Done.")}}, result)
			assert.Equal(t, test.expected.loops, execCtx.Stats().GetCounter(gent.SCRepetitionLoopTotal))
		})
	}
}

func TestNativeAgent_ToolLimits(t *testing.T) {
	type input struct{ max float64 }
	type expected struct {
		effects []string
		key     gent.StatKey
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"first call", input{0}, expected{nil, gent.SCToolCallsFor + "first"}},
		{"later call", input{1}, expected{[]string{"first", "second"}, gent.SCToolCallsFor + "second"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var effects []string
			model := newMockModel(nativeTestResponse(&gent.ContentChoice{Completion: gent.ModelCompletion{
				Status: gent.ModelStatusToolCalls,
			}, Parts: []gent.ContentPart{
				nativeTestCall("a", "first", "{}"), nativeTestCall("b", "second", "{}")}}),
				nativeTestResponse(&gent.ContentChoice{Completion: gent.ModelCompletion{
					Status: gent.ModelStatusToolCalls,
				}, Parts: []gent.ContentPart{
					nativeTestCall("c", "second", "{}"), nativeTestCall("d", "first", "{}")}}))
			agent := NewAgent(model)
			chain := toolchain.New()
			for _, name := range []string{"first", "second"} {
				chain.RegisterTool(gent.DefineTool(name, func(context.Context, map[string]any) (
					*gent.ToolExecutionResponse, error,
				) {
					effects = append(effects, name)
					return capturedToolResponse("ok", nil)
				}).Description("Read").ParameterSchema(nil).MustBuild())
			}
			runner, execCtx := newTestExecution(t, agent, &gent.Config{ToolChain: chain,
				Limits: []gent.Limit{{Type: gent.LimitKeyPrefix,
					Key: gent.SCToolCallsFor, MaxValue: test.input.max}},
			})
			_, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Read.")))
			if !assert.NoError(t, err) || !assert.NotNil(t, result.ExceededLimit) {
				return
			}
			assert.Equal(t, gent.ExecutionPaused, result.Status)
			assert.Equal(t, test.expected, expected{effects, result.ExceededLimit.MatchedKey})
		})
	}
}

func TestNativeAgent_MissingCallID(t *testing.T) {
	call := nativeTestCall("", "lookup", "{}")
	call.Type = ""
	model := newMockModel(
		nativeTestResponse(
			&gent.ContentChoice{

				Completion: gent.ModelCompletion{
					Status: gent.ModelStatusToolCalls,
				},
				Parts: []gent.ContentPart{
					call,
				},
			},
		),
		nativeTestResponse(answerChoice("Done.")),
	)
	chain := toolchain.New().RegisterTool(
		gent.DefineTool("lookup",
			func(context.Context, map[string]any) (*gent.ToolExecutionResponse, error) {
				return capturedToolResponse("ok", nil)
			}).Description("Read").
			ParameterSchema(nil).MustBuild())
	runner, execCtx := newTestExecution(t, NewAgent(model), &gent.Config{ToolChain: chain})
	value, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Read.")))
	if !assert.NoError(t, err) {
		return
	}
	assert.Equal(t, "Done.", value)
	assert.Equal(t, gent.ExecutionFinished, result.Status)
	saved := model.capturedMessages[1][1].Parts[0].(*gent.ModelToolCallRequest)
	assert.NotEmpty(t, saved.ID)
	assert.Equal(t, "function", saved.Type)
	assert.Equal(t, &gent.ModelMessage{Role: gent.ChatMessageTypeTool, Parts: []gent.ContentPart{
		preparedTestReply(saved.ID, "lookup", "ok")}}, model.capturedMessages[1][2])
}
func (m *nativeFinalModel) PrepareRequest(execCtx *gent.ExecutionContext,
	request *gent.ModelCallRequest, chain gent.ToolChain,
) (*gent.ModelCallRequest, error) {
	return gent.PrepareModelRequest(execCtx, request, chain)
}
