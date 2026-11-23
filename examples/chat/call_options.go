package main

import "github.com/rickchristie/gent"

// These controls belong to the example's main model calls. They request provider-exposed
// reasoning, not hidden reasoning. Some providers and simple tasks return no readable summary.
// Keep the same settings across turns so native continuation remains usable after restoration.
func chatCallOptions(provider string, model gent.Model) gent.CallOption {
	return func(options *gent.CallOptions) {
		options.MaxTokens = 8192
		switch provider {
		case "openai":
			options.Metadata = map[string]any{"responses": map[string]any{
				"reasoning": map[string]any{"effort": "medium", "summary": "auto"},
			}}
		case "anthropic":
			// Thinking on Sonnet 4.6 permits only temperature 1. Models that forbid sampling
			// parameters must still omit them; ReAct resolves their defaults from ModelName.
			if gent.DefaultSamplingParams(model).Temperature.Directive != gent.ParamForbidden {
				gent.WithTemperature(1)(options)
			}
			options.Metadata = map[string]any{"anthropic": map[string]any{
				"thinking":      map[string]any{"type": "adaptive"},
				"output_config": map[string]any{"effort": "medium"},
			}}
		case "gemini":
			options.Metadata = map[string]any{"thinking_config": &gent.ThinkingConfig{
				ReturnThinking: true, StreamThinking: true,
			}}
		}
		// xAI uses the selected model's default reasoning settings. A reasoning token count or
		// encrypted continuation is not readable text and must not become a synthetic UI message.
	}
}
