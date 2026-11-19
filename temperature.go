package gent

import (
	core "github.com/rickchristie/gent/internal/core"
)

// ModelNamer is an optional interface that models can implement to expose their name.
// This is used by [DefaultSamplingParams] to select model-appropriate defaults.
//
// Provider adapters in the models package implement this interface.
type ModelNamer = core.ModelNamer

// ParamDirective indicates how a sampling parameter should be handled for a given model.
type ParamDirective = core.ParamDirective

// ParamOmit means the parameter should not be sent. The API will use its own default.
// The empty value is the default. Use it when the model works without the parameter and the API
// default is acceptable (e.g., top-p for Gemini 3 which uses thinking_level instead).
const ParamOmit ParamDirective = core.ParamOmit

// ParamOverride means the parameter should be sent with the specified Value. This is
// appropriate for model-specific defaults that differ from the API default (e.g.,
// temperature 0.2 for Claude).
const ParamOverride ParamDirective = core.ParamOverride

// ParamForbidden means the parameter MUST NOT be sent. The API will return an error if
// it receives this parameter (e.g., temperature for OpenAI o-series). If a user
// explicitly sets a forbidden parameter, a warning event should be published.
const ParamForbidden ParamDirective = core.ParamForbidden

// SamplingParam is a single sampling parameter with its directive and value.
type SamplingParam = core.SamplingParam

// SamplingParams contains model-appropriate default sampling parameters for an LLM call.
// Each parameter carries a [ParamDirective] indicating whether it should be sent.
//
// Use [DefaultSamplingParams] to obtain defaults for a model.
//
// # Model-Specific Defaults
//
// Derived from vendor documentation and empirical findings in .concepts/10-TEMPERATURE.md:
//
//	┌──────────────────────┬──────────────┬──────────────┐
//	│ Model                │ Temperature  │ TopP         │
//	├──────────────────────┼──────────────┼──────────────┤
//	│ OpenAI reasoning     │ forbidden    │ forbidden    │
//	│ (o1/o3/o4/gpt-5/6)   │              │              │
//	├──────────────────────┼──────────────┼──────────────┤
//	│ Claude Fable 5.1     │ forbidden    │ forbidden    │
//	├──────────────────────┼──────────────┼──────────────┤
//	│ Other Claude models  │ override 0.2 │ omit         │
//	├──────────────────────┼──────────────┼──────────────┤
//	│ Grok 4.7             │ override 0.7 │ override 0.95│
//	├──────────────────────┼──────────────┼──────────────┤
//	│ Gemini 3+            │ override 1.0 │ omit         │
//	├──────────────────────┼──────────────┼──────────────┤
//	│ DeepSeek-R1          │ override 0.6 │ override 0.95│
//	├──────────────────────┼──────────────┼──────────────┤
//	│ Qwen3                │ override 0.6 │ override 0.95│
//	├──────────────────────┼──────────────┼──────────────┤
//	│ All others           │ override 0.2 │ override 1.0 │
//	└──────────────────────┴──────────────┴──────────────┘
type SamplingParams = core.SamplingParams

// DefaultSamplingParams returns model-appropriate default sampling parameters based on
// the model's name.
//
// If the model does not implement [ModelNamer] or the name is empty, the generic defaults
// are returned (temperature 0.2, top-p 1.0).
//
// This function is designed to be called by Loop implementations to set sensible
// defaults. User-provided options (via WithCallOptions or similar) should always override
// the values returned here.
func DefaultSamplingParams(model Model) SamplingParams {
	return core.DefaultSamplingParams(model)
}
