package examples

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/auth"
	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/executor"
	"github.com/rickchristie/gent/output"
	"github.com/stretchr/testify/assert"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (call roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return call(request)
}

func TestHelloWorld(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "fixture-key")
	t.Setenv("GENT_MODEL", "fixture-model")
	oldClient, oldOutput := http.DefaultClient, os.Stdout
	reader, writer, err := os.Pipe()
	if !assert.NoError(t, err) {
		return
	}
	t.Cleanup(func() {
		http.DefaultClient, os.Stdout = oldClient, oldOutput
		assert.NoError(t, reader.Close())
	})
	var requests []map[string]any
	http.DefaultClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (
		*http.Response, error,
	) {
		assert.Equal(t, "https://api.openai.com/v1/responses", request.URL.String())
		assert.Equal(t, "Bearer fixture-key", request.Header.Get("Authorization"))
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			return nil, err
		}
		requests = append(requests, body)
		return &http.Response{StatusCode: 200, Header: http.Header{
			"Content-Type": []string{"application/json"},
		}, Body: io.NopCloser(strings.NewReader(`{
  "status": "completed",
  "output": [{"type": "message", "id": "answer", "role": "assistant",
    "phase": "final_answer", "content": [{"type": "output_text", "text": "Hello. I am Gent."}]}],
  "usage": {"input_tokens": 4, "output_tokens": 5}
}`))}, nil
	})}
	os.Stdout = writer
	callErr := HelloWorld()
	assert.NoError(t, writer.Close())
	os.Stdout = oldOutput
	printed, readErr := io.ReadAll(reader)
	assert.NoError(t, readErr)
	assert.NoError(t, callErr)
	assert.Equal(t, `Status: finished
Answer: Hello. I am Gent.
`, string(printed))
	if !assert.Len(t, requests, 1) {
		return
	}
	assert.Equal(t, "fixture-model", requests[0]["model"])
	assert.Equal(t, []any{map[string]any{"role": "user", "content": []any{
		map[string]any{"type": "input_text", "text": "Hello! Who are you?"},
	}}}, requests[0]["input"])
}

type fixtureToken struct{}

func (fixtureToken) Token(context.Context) (*auth.Token, error) {
	return &auth.Token{Value: "fixture", Expiry: time.Now().Add(time.Hour)}, nil
}

func TestProviderConstruction(t *testing.T) {
	type input struct {
		make func(string, string) gent.Model
	}
	type expected struct {
		name string
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"openai", input{openAI}, expected{"fixture"}},
		{"anthropic", input{anthropic}, expected{"fixture"}},
		{"gemini", input{gemini}, expected{"fixture"}},
		{"xai", input{xai}, expected{"fixture"}},
		{"openrouter", input{openRouter}, expected{"fixture"}},
		{"deepseek", input{deepSeek}, expected{"fixture"}},
		{"ollama", input{ollama}, expected{"fixture"}},
		{"vllm", input{vllm}, expected{"fixture"}},
		{"llamacpp", input{llamaCpp}, expected{"fixture"}},
		{"sglang", input{sglang}, expected{"fixture"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			model := test.input.make("http://localhost:9999", "fixture")
			assert.Equal(t, test.expected.name, model.(gent.ModelNamer).ModelName())
		})
	}
	model, err := vertex("fixture-project", "fixture", fixtureToken{})
	assert.NoError(t, err)
	assert.Equal(t, "fixture", model.(gent.ModelNamer).ModelName())
	endpoint := customEndpoint("http://localhost", "key", "fixture")
	assert.Equal(t, "fixture", endpoint.(gent.ModelNamer).ModelName())
}

type scriptModel struct {
	Calls    []*gent.ModelToolCallRequest
	Requests []*gent.ModelCallRequest
	Answer   string
}

func (model *scriptModel) PrepareRequest(execCtx *gent.ExecutionContext,
	request *gent.ModelCallRequest, chain gent.ToolChain,
) (*gent.ModelCallRequest, error) {
	return gent.PrepareModelRequest(execCtx, request, chain)
}

func (model *scriptModel) GenerateContentStream(ctx context.Context,
	request *gent.ModelCallRequest,
) (
	gent.Stream, error,
) {
	model.Requests = append(model.Requests, request)
	if len(model.Requests) != 1 || len(model.Calls) == 0 {
		return (&FixedModel{Text: model.Answer}).GenerateContentStream(ctx, request)
	}
	parts := make([]gent.ContentPart, len(model.Calls))
	for index, call := range model.Calls {
		parts[index] = *call
	}
	return gent.NewCompletedStream(&gent.ModelCallResponse{Choices: []*gent.ContentChoice{{
		Completion: gent.ModelCompletion{Status: gent.ModelStatusToolCalls}, Parts: parts,
	}}}, nil), nil
}

func toolCall(name, arguments string) *gent.ModelToolCallRequest {
	return &gent.ModelToolCallRequest{ID: "call-1", Type: "function",
		FunctionCall: &gent.FunctionCall{Name: name, Arguments: arguments}}
}

func runText(t *testing.T, runner *executor.Runner[string]) (string, *gent.ExecutionResult) {
	t.Helper()
	execCtx, err := runner.NewExecutionContext(context.Background(), "example")
	if !assert.NoError(t, err) {
		return "", nil
	}
	defer execCtx.Release()
	answer, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Run the example.")))
	assert.NoError(t, err)
	assert.Equal(t, &gent.ExecutionResult{Status: gent.ExecutionFinished,
		TurnId: execCtx.TurnId(), TerminationReason: gent.TerminationSuccess,
		Output: []gent.ContentPart{gent.TextPart(answer)}}, result)
	return answer, result
}

func TestToolsAndConversations(t *testing.T) {
	model := &scriptModel{Calls: []*gent.ModelToolCallRequest{
		toolCall("add", `{"a":19,"b":23}`),
	}, Answer: "42"}
	answer, _ := runText(t, toolsExample(model))
	assert.Equal(t, "42", answer)
	if !assert.Len(t, model.Requests, 2) {
		return
	}
	var replies []string
	for _, message := range model.Requests[1].Messages {
		for _, part := range message.Parts {
			if reply, ok := part.(*gent.ModelToolCallResponse); ok {
				replies = append(replies, gent.ContentTextData(reply.Parts()))
			}
		}
	}
	assert.Equal(t, []string{"42"}, replies)
	answers, err := conversation(context.Background(), &FixedModel{Text: "River"})
	assert.Equal(t, []string{"River", "River", "River"}, answers)
	assert.NoError(t, err)
	runner := executor.NewRunner(reactOptions(&FixedModel{Text: "Ready."}), &gent.Config{})
	answer, _ = runText(t, runner)
	assert.Equal(t, "Ready.", answer)
	assert.NoError(t, independentConversations(context.Background(), runner))
}

func TestToolResponses(t *testing.T) {
	type input struct {
		call func() (*gent.ToolExecutionResponse, error)
	}
	type expected struct {
		response *gent.ToolExecutionResponse
		err      error
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"blocked", input{blockedTool}, expected{&gent.ToolExecutionResponse{
			Status: gent.ToolBlocked, Instructions: "Ask the user to approve the shipment."}, nil}},
		{"invalid", input{invalidTool}, expected{&gent.ToolExecutionResponse{
			Status:       gent.ToolInvalidArguments,
			Instructions: "Supply an order ID from the current account."}, nil}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			response, err := test.input.call()
			assert.Equal(t, test.expected, expected{response, err})
		})
	}
	data, err := structuredTool(42)
	assert.NoError(t, err)
	assert.Equal(t, gent.ToolExecuted, data.Status)
	assert.Equal(t, `{"sum":42}`, gent.ContentTextData(data.Parts))
	assert.Equal(t, "Use the returned sum in your answer.", data.Instructions)
	failed, err := failedTool().Call(context.Background(), struct{}{})
	assert.Nil(t, failed)
	assert.EqualError(t, err, "order store connection failed")
	for _, create := range []func() (gent.Tool[*lookupInput], error){
		func() (gent.Tool[*lookupInput], error) { return boundTool(&orderService{}) },
		schemaExamples, explicitSchema, outputSchema,
	} {
		tool, err := create()
		assert.NoError(t, err)
		response, err := tool.Call(context.Background(), &lookupInput{ID: "ABC"})
		assert.NoError(t, err)
		assert.Equal(t, &gent.ToolExecutionResponse{Status: gent.ToolExecuted,
			Parts: []gent.ContentPart{gent.TextPart("ABC")}}, response)
	}
	optional, err := optionalTool()
	assert.NoError(t, err)
	response, err := optional.Call(context.Background(), SearchInput{Limit: 3})
	assert.NoError(t, err)
	expectedResponse, err := structuredTool(3)
	assert.NoError(t, err)
	assert.Equal(t, expectedResponse, response)
}

func TestOutputExamples(t *testing.T) {
	runner, err := jsonRunner(&FixedModel{Text: `{"summary":"Ready","count":2}`})
	if !assert.NoError(t, err) {
		return
	}
	execCtx, err := runner.NewExecutionContext(context.Background(), "json")
	if !assert.NoError(t, err) {
		return
	}
	defer execCtx.Release()
	value, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Summarize.")))
	assert.NoError(t, err)
	assert.NoError(t, checkOutputResult(result))
	assert.Equal(t, Answer{Summary: "Ready", Count: 2}, value)
	runCheck(t, &gent.Config{}, func(execCtx *gent.ExecutionContext) error {
		out, err := schemaOutput()
		assert.NoError(t, err)
		request := gent.NewModelCallRequest(nil)
		assert.NoError(t, out.Configure(request))
		processed, err := out.Process(execCtx, []gent.ContentPart{gent.TextPart(
			`{"summary":"Ready","count":2}`)})
		assert.NoError(t, err)
		assert.Equal(t, &gent.OutputResult[Answer]{Accepted: true, Value: value,
			Content: []gent.ContentPart{gent.TextPart(`{"summary":"Ready","count":2}`)}}, processed)

		type input struct{ text string }
		type expected struct {
			accepted bool
			value    string
			steering []gent.ContentPart
		}
		type testCase struct {
			name     string
			input    input
			expected expected
		}
		cases := []testCase{
			{"valid", input{"Ready"}, expected{true, "Ready", nil}},
			{"empty", input{" "}, expected{false, "", []gent.ContentPart{
				gent.TextPart("Return a non-empty answer.")}}},
		}
		for _, test := range cases {
			t.Run(test.name, func(t *testing.T) {
				parts := []gent.ContentPart{gent.TextPart(test.input.text)}
				got, err := textOutput().Process(execCtx, parts)
				assert.NoError(t, err)
				assert.Equal(t, test.expected, expected{got.Accepted, got.Value, got.Steering})
			})
		}
		custom := &TrimmedOutput{}
		assert.NoError(t, custom.Configure(request))
		trimmed, err := custom.Process(execCtx, []gent.ContentPart{gent.TextPart(" Ready. ")})
		assert.NoError(t, err)
		assert.Equal(t, &gent.OutputResult[string]{Accepted: true, Value: "Ready.",
			Content: []gent.ContentPart{gent.TextPart("Ready.")}}, trimmed)
		assert.Equal(t, "Return a short text answer.", custom.Instructions())
		assert.Equal(t, &gent.Task{Instructions: []*gent.ModelMessage{
			gent.TextParts(gent.ChatMessageTypeSystem, "Return the answer as text.")},
			Messages: gent.NewTask(gent.TextPart("Describe the result.")).Messages},
			outputInstructions(output.NewTextOutput()))
		assert.NoError(t, imageOutput().Configure(request))
		invalid := []gent.ContentPart{gent.PNGPart([]byte("invalid"))}
		checkedImage, err := imageOutput().Process(execCtx, invalid)
		assert.NoError(t, err)
		assert.False(t, checkedImage.Accepted)
		var encoded bytes.Buffer
		assert.NoError(t, png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 1, 1))))
		part := gent.PNGPart(encoded.Bytes())
		checkedImage, err = imageOutput().Process(execCtx, []gent.ContentPart{part})
		assert.NoError(t, err)
		assert.Equal(t, &gent.OutputResult[[]gent.BinaryContent]{Accepted: true,
			Value:   []gent.BinaryContent{part.(gent.BinaryContent)},
			Content: []gent.ContentPart{part}}, checkedImage)
		return nil
	})
}

func TestContentAndTasks(t *testing.T) {
	tasks := taskExamples()
	assert.Equal(t, []*gent.Task{
		gent.NewTask(gent.TextPart("Explain how a Go channel works.")),
		{Instructions: []*gent.ModelMessage{
			gent.TextParts(gent.ChatMessageTypeSystem, "Use short explanations.")},
			Messages: gent.NewTask(gent.TextPart("What is a channel?")).Messages},
		gent.NewTask(gent.TextPart("Show an example.")),
		{Instructions: []*gent.ModelMessage{},
			Messages: gent.NewTask(gent.TextPart("Explain it another way.")).Messages},
	}, tasks)
	parts, err := mediaParts([]byte{1}, []byte{2}, []byte{3}, []byte{4})
	assert.NoError(t, err)
	assert.Equal(t, gent.TextPart("Read the attached files."), parts[0])
	assert.Equal(t, `{"order_id":"ABC123"}`, gent.ContentTextData(parts[1:2]))
	assert.Equal(t, []gent.ContentPart{gent.PNGPart([]byte{1}), gent.PDFPart([]byte{2}),
		gent.AudioPart("audio/wav", []byte{3}), gent.VideoPart("video/mp4", []byte{4})}, parts[2:])
	assert.Equal(t, gent.NewTask(gent.TextPart("Describe this image."),
		gent.ImageURLPart("https://example.com/image.png")), imageURL())
	assert.Equal(t, gent.CachedContent{ContentPart: gent.TextPart(
		"Use the account rules supplied by the application."),
		CacheControl: &gent.CacheControl{Type: "ephemeral"}}, cachedPart())
}

func TestReferenceLoopAndCustomLoop(t *testing.T) {
	// docs:start test-agent
	runner := executor.NewRunner(&LocalLoop{}, &gent.Config{})
	execCtx, err := runner.NewExecutionContext(context.Background(), "test")
	if !assert.NoError(t, err) {
		return
	}
	defer execCtx.Release()
	answer, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Start.")))
	assert.NoError(t, err)
	assert.Equal(t, "Ready.", answer)
	assert.Equal(t, &gent.ExecutionResult{
		Status: gent.ExecutionFinished, TurnId: execCtx.TurnId(),
		TerminationReason: gent.TerminationSuccess,
		Output:            []gent.ContentPart{gent.TextPart("Ready.")},
	}, result)
	// docs:end test-agent
	stream, err := streamExample(context.Background())
	assert.NoError(t, err)
	response, err := stream.Response()
	assert.NoError(t, err)
	assert.Equal(t, []gent.ContentPart{gent.TextPart("Ready.")},
		response.Choices[0].CandidateContent())
}

type checkLoop struct {
	check func(*gent.ExecutionContext) error
}

func (*checkLoop) Output() gent.Output[string] { return output.NewTextOutput() }

func (loop *checkLoop) Next(execCtx *gent.ExecutionContext) (*gent.AgentLoopResult, error) {
	if err := loop.check(execCtx); err != nil {
		return nil, err
	}
	return (&LocalLoop{}).Next(execCtx)
}

func runCheck(t *testing.T, config *gent.Config, check func(*gent.ExecutionContext) error) {
	t.Helper()
	answer, _ := runText(t, executor.NewRunner(&checkLoop{check: check}, config))
	assert.Equal(t, "Ready.", answer)
}
