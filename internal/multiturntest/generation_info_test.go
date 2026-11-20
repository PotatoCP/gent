package multiturntest

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/rickchristie/gent"
	"github.com/stretchr/testify/assert"
)

func TestProviderGenerationInfo(t *testing.T) {
	type input struct{ provider, format string }
	type expected struct {
		info gent.GenerationInfo
		err  error
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	var cases []testCase
	for _, provider := range testProviders {
		for _, format := range []string{"json", "stream", "missing"} {
			want := generationInfoExpected(provider, format)
			cases = append(cases, testCase{
				provider + "/" + format, input{provider, format}, expected{want, nil},
			})
		}
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			body, mime := generationInfoFixture(t, test.input.provider, test.input.format)
			client := &http.Client{Transport: responseTransport(func(request *http.Request) (
				*http.Response, error,
			) {
				if request.Method == http.MethodGet {
					return fixtureHTTP(routerFixture, "application/json"), nil
				}
				response := fixtureHTTP(body, mime)
				if test.input.format != "missing" {
					header := "X-Request-Id"
					if test.input.provider == "anthropic" {
						header = "Request-Id"
					}
					response.Header.Set(header, "request-42")
				}
				return response, nil
			})}
			model := fixtureModel(test.input.provider, client)
			started := time.Now()
			request := gent.NewModelCallRequest([]*gent.ModelMessage{
				gent.TextParts(gent.ChatMessageTypeHuman, "Finish the task."),
			})
			stream, err := model.GenerateContentStream(context.Background(), request)
			if !assert.Equal(t, test.expected.err, err) {
				return
			}
			defer stream.Close()
			for range stream.Chunks() {
			}
			response, err := stream.Response()
			if !assert.Equal(t, test.expected.err, err) || !assert.NotNil(t, response) {
				return
			}
			info := *response.Info
			assert.GreaterOrEqual(t, info.TimeToFirstContent, time.Nanosecond)
			assert.GreaterOrEqual(t, info.Duration, info.TimeToFirstContent)
			assert.LessOrEqual(t, info.Duration, time.Since(started))
			info.Duration, info.TimeToFirstContent = 0, 0
			assert.Equal(t, test.expected.info, info)
		})
	}
}

func generationInfoExpected(provider, format string) gent.GenerationInfo {
	info := gent.GenerationInfo{InputTokens: 20, OutputTokens: 7, TotalTokens: 27,
		CachedInputTokens: 5, CacheWriteInputTokens: 3, ReasoningTokens: 2,
		ProviderRequestId: "request-42", ProviderResponseId: "response-42",
		ResponseModel: "returned-model-v2", ResponseServiceTier: "priority"}
	if format == "missing" {
		info = gent.GenerationInfo{}
	}
	profile := ""
	switch provider {
	case "openai", "xai":
		return info
	case "anthropic":
		profile = "anthropic"
		info.ReasoningTokens = 0
	case "gemini", "vertexai":
		profile = "gemini"
		info.CacheWriteInputTokens = 0
	case "vllm", "sglang":
		profile = "xgrammar"
	case "llamacpp", "ollama":
		profile = "llamacpp"
	}
	if provider == "ollama" {
		info.CacheWriteInputTokens, info.ReasoningTokens = 0, 0
		info.ProviderResponseId, info.ResponseServiceTier = "", ""
	}
	info.RawGenerationInfo = map[string]any{"output_schema": map[string]any{
		"native": false, "profile": profile, "reason": "no output schema requested",
	}}
	if provider == "anthropic" {
		info.RawGenerationInfo["cache_creation_input_tokens"] = info.CacheWriteInputTokens
	}
	return info
}
