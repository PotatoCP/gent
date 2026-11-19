package models

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/stretchr/testify/assert"
)

var testProviders = []string{"openai", "anthropic", "gemini", "vertexai", "xai", "deepseek",
	"vllm", "ollama", "openrouter", "llamacpp", "sglang"}

func streamFixture(t *testing.T, provider, turn string) (string, string) {
	t.Helper()
	protocol, extension, mime := provider, "sse", "text/event-stream"
	switch provider {
	case "vertexai":
		protocol = "gemini"
	case "openai", "xai":
		protocol = "responses"
	case "deepseek", "vllm", "openrouter", "llamacpp", "sglang":
		protocol = "chat"
	case "ollama":
		extension, mime = "ndjson", "application/x-ndjson"
	}
	path := filepath.Join("testdata", "output-"+protocol+"-"+turn+"."+extension)
	data, err := os.ReadFile(path)
	if !assert.NoError(t, err) {
		t.FailNow()
	}
	return string(data), mime
}

func collectProviderMeta(t *testing.T, stream gent.Stream) []gent.ContentPart {
	t.Helper()
	var result []gent.ContentPart
	for chunk := range stream.Chunks() {

		result = append(result, chunk.Content...)
	}
	return result
}
