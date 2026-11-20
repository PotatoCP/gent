package multiturntest

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Use explicit native fields, independent of the adapter's private response structs.
const metadataResponses = `{
  "id":"response-42", "model":"returned-model-v2", "service_tier":"priority",
  "status":"completed", "output":[{"type":"message", "role":"assistant",
    "phase":"final_answer", "content":[{"type":"output_text", "text":"Done."}]}],
  "usage":{"input_tokens":20, "output_tokens":7, "total_tokens":27,
    "input_tokens_details":{"cached_tokens":5, "cache_write_tokens":3},
    "output_tokens_details":{"reasoning_tokens":2}}
}`

const metadataChat = `{
  "id":"response-42", "model":"returned-model-v2", "service_tier":"priority",
  "choices":[{"index":0,"message":{"role":"assistant","content":"Done."},
    "finish_reason":"stop"}],
  "usage":{"prompt_tokens":20, "completion_tokens":7, "total_tokens":27,
    "prompt_tokens_details":{"cached_tokens":5, "cache_write_tokens":3},
    "completion_tokens_details":{"reasoning_tokens":2}}
}`

const metadataAnthropic = `{
  "id":"response-42", "model":"returned-model-v2", "stop_reason":"end_turn",
  "content":[{"type":"text", "text":"Done."}],
  "usage":{"input_tokens":12, "output_tokens":7,
    "cache_read_input_tokens":5, "cache_creation_input_tokens":3,
    "cache_creation":{"ephemeral_5m_input_tokens":3}, "service_tier":"priority"}
}`

const metadataGemini = `{
  "responseId":"response-42", "modelVersion":"returned-model-v2",
  "candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"Done."}]},
    "finishReason":"STOP"}],
  "usageMetadata":{"promptTokenCount":20,"candidatesTokenCount":5,"totalTokenCount":27,
    "cachedContentTokenCount":5,"thoughtsTokenCount":2,"serviceTier":"priority"}
}`

const metadataOllama = `{
  "model":"returned-model-v2", "message":{"role":"assistant", "content":"Done."},
  "done":true, "done_reason":"stop", "prompt_eval_count":20,
  "prompt_eval_cached_count":5, "eval_count":7
}`

func generationInfoFixture(t *testing.T, provider, format string) (string, string) {
	t.Helper()
	body := metadataChat
	switch provider {
	case "openai", "xai":
		body = metadataResponses
	case "anthropic":
		body = metadataAnthropic
	case "gemini", "vertexai":
		body = metadataGemini
	case "ollama":
		body = metadataOllama
	}
	if format == "missing" {
		var value map[string]json.RawMessage
		if !assert.NoError(t, json.Unmarshal([]byte(body), &value)) {
			t.FailNow()
		}
		for _, name := range []string{"id", "model", "service_tier", "usage", "responseId",
			"modelVersion", "usageMetadata", "prompt_eval_count", "prompt_eval_cached_count",
			"eval_count"} {
			delete(value, name)
		}
		encoded, err := json.Marshal(value)
		if !assert.NoError(t, err) {
			t.FailNow()
		}
		return string(encoded), "application/json"
	}
	if format == "json" {
		return body, "application/json"
	}
	switch provider {
	case "openai", "xai":
		return metadataSSE(
			`{"type":"response.output_text.delta","output_index":0,"delta":"Done."}`,
			`{"type":"response.completed","response":`+body+`}`,
		), "text/event-stream"
	case "anthropic":
		return metadataSSE(
			`{"type":"message_start","message":{"id":"response-42",
"model":"returned-model-v2","content":[],"usage":{"input_tokens":12,"output_tokens":1,
"cache_read_input_tokens":5,"cache_creation_input_tokens":3,"service_tier":"standard"}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Done."}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"message_delta","delta":{"stop_reason":null},"usage":{"output_tokens":4}}`,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn"},
"usage":{"output_tokens":7,"cache_creation":{"ephemeral_5m_input_tokens":3},
"service_tier":"priority"}}`,
			`{"type":"message_stop"}`,
		), "text/event-stream"
	case "gemini", "vertexai":
		return metadataSSE(
			`{"responseId":"response-42","modelVersion":"returned-model-v2",
"candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"Done."}]}}],
"usageMetadata":{"promptTokenCount":20,"candidatesTokenCount":1,"serviceTier":"priority"}}`,
			`{"candidates":[{"index":0,"content":{"role":"model","parts":[]},
"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":20,"candidatesTokenCount":5,
"totalTokenCount":27,"cachedContentTokenCount":5,"thoughtsTokenCount":2}}`,
		), "text/event-stream"
	case "ollama":
		return strings.Join([]string{
			`{"model":"returned-model-v2","message":{"role":"assistant","content":"Done."}}`,
			`{"message":{"role":"assistant","content":""},"done":true,"done_reason":"stop",` +
				`"prompt_eval_count":20,"prompt_eval_cached_count":5,"eval_count":7}`,
		}, "\n"), "application/x-ndjson"
	default:
		return metadataSSE(
			`{"id":"response-42","model":"returned-model-v2","service_tier":"priority",
"choices":[{"index":0,"delta":{"content":"Done."},"finish_reason":null}],
"usage":{"prompt_tokens":20,"completion_tokens":1,"total_tokens":21}}`,
			`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			`{"choices":[],"usage":{"prompt_tokens":20,"completion_tokens":7,"total_tokens":27,
"prompt_tokens_details":{"cached_tokens":5,"cache_write_tokens":3},
"completion_tokens_details":{"reasoning_tokens":2}}}`,
			`[DONE]`,
		), "text/event-stream"
	}
}

// SSE permits a JSON event across multiple data lines. Keep the Go fixtures readable.
func metadataSSE(events ...string) string {
	var result strings.Builder
	for _, event := range events {
		for _, line := range strings.Split(event, "\n") {
			result.WriteString("data: ")
			result.WriteString(line)
			result.WriteByte('\n')
		}
		result.WriteByte('\n')
	}
	return result.String()
}
