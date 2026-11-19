package examples

import (
	"net/http"
	"time"

	"cloud.google.com/go/auth"
	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/agents"
	"github.com/rickchristie/gent/agents/react"
	"github.com/rickchristie/gent/format"
	"github.com/rickchristie/gent/models"
)

func openAI(apiKey, modelName string) gent.Model {
	// docs:start model-openai
	model := models.NewOpenAI(apiKey, modelName)
	// docs:end model-openai
	return model
}

func anthropic(apiKey, modelName string) gent.Model {
	// docs:start model-anthropic
	model := models.NewAnthropic(&models.Config{APIKey: apiKey, Model: modelName})
	// docs:end model-anthropic
	return model
}

func gemini(apiKey, modelName string) gent.Model {
	// docs:start model-gemini
	model := models.NewGemini(&models.Config{APIKey: apiKey, Model: modelName})
	// docs:end model-gemini
	return model
}

func xai(apiKey, modelName string) gent.Model {
	// docs:start model-xai
	model := models.NewXAI(apiKey, modelName)
	// docs:end model-xai
	return model
}

func openRouter(apiKey, modelName string) gent.Model {
	// docs:start model-openrouter
	model := models.NewOpenRouter(&models.Config{APIKey: apiKey, Model: modelName})
	// docs:end model-openrouter
	return model
}

func deepSeek(apiKey, modelName string) gent.Model {
	// docs:start model-deepseek
	model := models.NewDeepSeek(&models.Config{APIKey: apiKey, Model: modelName})
	// docs:end model-deepseek
	return model
}

func ollama(baseURL, modelName string) gent.Model {
	// docs:start model-ollama
	model := models.NewOllama(&models.Config{BaseURL: baseURL, Model: modelName})
	// docs:end model-ollama
	return model
}

func vllm(baseURL, modelName string) gent.Model {
	// docs:start model-vllm
	model := models.NewVLLM(&models.Config{BaseURL: baseURL, Model: modelName})
	// docs:end model-vllm
	return model
}

func llamaCpp(baseURL, modelName string) gent.Model {
	// docs:start model-llamacpp
	model := models.NewLlamaCpp(&models.Config{BaseURL: baseURL, Model: modelName})
	// docs:end model-llamacpp
	return model
}

func sglang(baseURL, modelName string) gent.Model {
	// docs:start model-sglang
	model := models.NewSGLang(&models.Config{BaseURL: baseURL, Model: modelName})
	// docs:end model-sglang
	return model
}

func vertex(project, modelName string, tokens auth.TokenProvider) (gent.Model, error) {
	// docs:start model-vertex
	model, err := models.NewVertexAI(&models.VertexAIConfig{
		Project:       project,
		Location:      "global",
		Model:         modelName,
		TokenProvider: tokens,
	})
	if err != nil {
		return nil, err
	}
	// Omit TokenProvider to use Application Default Credentials instead.
	// docs:end model-vertex
	return model, nil
}

func customEndpoint(baseURL, apiKey, modelName string) gent.Model {
	// docs:start model-endpoint
	model := models.NewResponses(&models.ResponsesConfig{
		Provider:   "openai",
		BaseURL:    baseURL,
		APIKey:     apiKey,
		Model:      modelName,
		HTTPClient: &http.Client{Timeout: 90 * time.Second},
	})
	// Provider identifies native continuation data. Use a compatible endpoint.
	// docs:end model-endpoint
	return model
}

func reactOptions(model gent.Model) *react.Agent[string] {
	// docs:start react-options
	loop := react.NewAgent(model).WithCallOptions(
		gent.WithMaxTokens(2048),
		gent.WithStreamThinking(true),
	)
	// Configure the loop before a Runner uses it. Reasoning support depends on the model.
	// docs:end react-options
	// docs:start react-format
	loop.WithToolResultFormat(format.NewMarkdown())
	// Formatting changes tool-result sections. It does not parse the final answer.
	// docs:end react-format
	// docs:start react-guards
	loop.WithMaxResponseChars(20000).
		WithResponseTooLongMessage("Return a shorter answer.").
		WithRepetitionConfig(agents.DefaultRepetitionConfig())
	// These guards provide correction guidance. Execution limits still apply.
	// docs:end react-guards
	return loop
}
