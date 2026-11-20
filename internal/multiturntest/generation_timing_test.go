package multiturntest

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/rickchristie/gent"
	"github.com/stretchr/testify/assert"
)

// Gates separate status, content, and completion. A gate opens only when the adapter asks for
// more bytes, after it has processed the previous event. No wall-clock sleeps are required.
type contentTimingBody struct {
	ctx       context.Context
	parts     []string
	current   *strings.Reader
	index     int
	ready     chan int
	advance   chan struct{}
	startedAt time.Time
}

func (body *contentTimingBody) Read(data []byte) (int, error) {
	for body.current == nil || body.current.Len() == 0 {
		if body.index == len(body.parts) {
			return 0, io.EOF
		}
		if body.index > 0 {
			select {
			case body.ready <- body.index:
			case <-body.ctx.Done():
				return 0, body.ctx.Err()
			}
			select {
			case <-body.advance:
			case <-body.ctx.Done():
				return 0, body.ctx.Err()
			}
		}
		body.current = strings.NewReader(body.parts[body.index])
		body.index++
	}
	return body.current.Read(data)
}

func (*contentTimingBody) Close() error { return nil }

func TestProviderFirstContentBoundary(t *testing.T) {
	type input struct {
		provider, mime          string
		status, content, finish string
	}
	type expected struct {
		hasContent bool
		err        error
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	responsesStatus := metadataSSE(`{"type":"response.created",
"response":{"status":"in_progress"}}`)
	responsesEmpty := metadataSSE(`{"type":"response.completed",
"response":{"status":"completed","output":[],"usage":{"input_tokens":20,"output_tokens":7}}}`)
	responsesTool := metadataSSE(`{"type":"response.completed","response":{"status":"completed",
"output":[{"type":"function_call","call_id":"call-42","name":"lookup","arguments":"{}"}],
"usage":{"input_tokens":20,"output_tokens":7}}}`)
	anthropicStatus := metadataSSE(`{"type":"message_start","message":{"id":"msg-42",
"content":[],"usage":{"input_tokens":20,"output_tokens":0}}}`, `{"type":"ping"}`)
	anthropicStop := metadataSSE(`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":7}}`,
		`{"type":"message_stop"}`)
	chatStatus := metadataSSE(`{"choices":[{"index":0,"delta":{"role":"assistant"}}]}`)
	chatEmpty := metadataSSE(
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`, `[DONE]`)
	geminiStatus := metadataSSE(`{"usageMetadata":{"promptTokenCount":20}}`)
	geminiStop := metadataSSE(`{"candidates":[{"index":0,"content":{"role":"model","parts":[]},
"finishReason":"STOP"}]}`)
	ollamaStatus := `{"message":{"role":"assistant","content":""},"done":false}
`
	ollamaStop := `{"message":{"role":"assistant","content":""},"done":true,"done_reason":"stop"}`
	cases := []testCase{
		{"Responses tool", input{"openai", "text/event-stream", responsesStatus,
			metadataSSE(`{"type":"response.output_item.added","output_index":0,
"item":{"type":"function_call","call_id":"call-42","name":"lookup","arguments":""}}`),
			responsesTool}, expected{true, nil}},
		{"Responses image", input{"openai", "text/event-stream", responsesStatus,
			metadataSSE(`{"type":"response.image_generation_call.partial_image",
"partial_image_b64":"aW1hZ2U="}`), responsesEmpty}, expected{true, nil}},
		{"Responses reasoning", input{"xai", "text/event-stream", responsesStatus,
			metadataSSE(`{"type":"response.reasoning_summary_text.delta",
"output_index":0,"delta":"Checking the record."}`), responsesEmpty}, expected{true, nil}},
		{"Responses empty", input{"openai", "text/event-stream", responsesStatus,
			metadataSSE(`{"type":"response.output_text.delta","output_index":0,"delta":""}`),
			responsesEmpty}, expected{false, nil}},
		{"Anthropic tool", input{"anthropic", "text/event-stream", anthropicStatus,
			metadataSSE(`{"type":"content_block_start","index":0,
"content_block":{"type":"tool_use","id":"call-42","name":"lookup","input":{}}}`),
			anthropicStop}, expected{true, nil}},
		{"Anthropic opaque", input{"anthropic", "text/event-stream", anthropicStatus,
			metadataSSE(`{"type":"content_block_start","index":0,
"content_block":{"type":"redacted_thinking","data":"opaque"}}`),
			strings.ReplaceAll(anthropicStop, `"tool_use"`, `"end_turn"`)}, expected{false, nil}},
		{"Chat tool", input{"deepseek", "text/event-stream", chatStatus,
			metadataSSE(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,
"id":"call-42","type":"function","function":{"name":"lookup","arguments":"{}"}}]}}]}`),
			strings.ReplaceAll(chatEmpty, `"stop"`, `"tool_calls"`)}, expected{true, nil}},
		{"Chat text", input{"vllm", "text/event-stream", chatStatus,
			metadataSSE(`{"choices":[{"index":0,"delta":{"content":"Done."}}]}`),
			chatEmpty}, expected{true, nil}},
		{"Chat reasoning", input{"sglang", "text/event-stream", chatStatus,
			metadataSSE(`{"choices":[{"index":0,"delta":{"reasoning_content":"Checking."}}]}`),
			chatEmpty}, expected{true, nil}},
		{"Chat opaque", input{"openrouter", "text/event-stream", chatStatus,
			metadataSSE(`{"choices":[{"index":0,"delta":{"reasoning_details":[
{"type":"reasoning.encrypted","data":"opaque"}]}}]}`), chatEmpty}, expected{false, nil}},
		{"Chat empty", input{"llamacpp", "text/event-stream", chatStatus,
			metadataSSE(`{"choices":[{"index":0,"delta":{"content":"","tool_calls":[]}}]}`),
			chatEmpty}, expected{false, nil}},
		{"Gemini tool", input{"gemini", "text/event-stream", geminiStatus,
			metadataSSE(`{"candidates":[{"index":0,"content":{"role":"model","parts":[
{"functionCall":{"id":"call-42","name":"lookup","args":{}}}]}}]}`),
			geminiStop}, expected{true, nil}},
		{"Vertex reasoning", input{"vertexai", "text/event-stream", geminiStatus,
			metadataSSE(`{"candidates":[{"index":0,"content":{"role":"model","parts":[
{"text":"Checking.","thought":true}]}}]}`), geminiStop}, expected{true, nil}},
		{"Gemini opaque", input{"gemini", "text/event-stream", geminiStatus,
			metadataSSE(`{"candidates":[{"index":0,"content":{"role":"model","parts":[
{"thoughtSignature":"opaque"}]}}]}`), geminiStop}, expected{false, nil}},
		{"Ollama tool", input{"ollama", "application/x-ndjson", ollamaStatus,
			`
{"message":{"role":"assistant","tool_calls":[{"function":{"name":"lookup","arguments":{}}}]}}
`,
			ollamaStop}, expected{true, nil}},
		{"Ollama thinking", input{"ollama", "application/x-ndjson", ollamaStatus,
			`{"message":{"role":"assistant","thinking":"Checking."}}
`,
			ollamaStop}, expected{true, nil}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			body := &contentTimingBody{
				ctx: ctx, ready: make(chan int), advance: make(chan struct{}),
				parts: []string{test.input.status, test.input.content, test.input.finish},
			}
			client := &http.Client{Transport: responseTransport(func(request *http.Request) (
				*http.Response, error,
			) {
				if request.Method == http.MethodGet {
					return fixtureHTTP(routerFixture, "application/json"), nil
				}
				body.startedAt = time.Now()
				return &http.Response{StatusCode: http.StatusOK, Body: body,
					Header: http.Header{"Content-Type": {test.input.mime}}}, nil
			})}
			model := fixtureModel(test.input.provider, client)
			started := time.Now()
			stream, err := model.GenerateContentStream(ctx, gent.NewModelCallRequest(
				[]*gent.ModelMessage{gent.TextParts(gent.ChatMessageTypeHuman, "Read the record.")},
			))
			if !assert.Equal(t, test.expected.err, err) {
				return
			}
			defer stream.Close()
			if !waitContentGate(t, ctx, body.ready, 1) {
				return
			}
			minimum := time.Since(body.startedAt)
			body.advance <- struct{}{}
			if !waitContentGate(t, ctx, body.ready, 2) {
				return
			}
			maximum := time.Since(started)
			body.advance <- struct{}{}
			for range stream.Chunks() {
			}
			response, err := stream.Response()
			if !assert.Equal(t, test.expected.err, err) || !assert.NotNil(t, response) {
				return
			}
			if test.expected.hasContent {
				assert.GreaterOrEqual(t, response.Info.TimeToFirstContent, minimum)
				assert.LessOrEqual(t, response.Info.TimeToFirstContent, maximum)
			} else {
				assert.Equal(t, time.Duration(0), response.Info.TimeToFirstContent)
			}
			assert.GreaterOrEqual(t, response.Info.Duration, response.Info.TimeToFirstContent)
		})
	}
}

func waitContentGate(t *testing.T, ctx context.Context, ready <-chan int, phase int) bool {
	t.Helper()
	select {
	case value := <-ready:
		return assert.Equal(t, phase, value)
	case <-ctx.Done():
		return assert.NoError(t, ctx.Err(), "provider did not read the next event")
	}
}
