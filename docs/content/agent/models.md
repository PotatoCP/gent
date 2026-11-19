The loop owns its model. Create the adapter once, configure the loop, then give the loop to a
Runner. The following examples assume `apiKey` and `modelName` come from your application settings.
Constructing an adapter does not run an agent task.

## OpenAI

{{example:model-openai}}

## Anthropic

{{example:model-anthropic}}

## Gemini

{{example:model-gemini}}

## Vertex AI

Supply `project`, `modelName`, and an uncached Google `auth.TokenProvider` named `tokens`. The
constructor loads credentials but does not request a model response. Omit `TokenProvider` to use
Application Default Credentials. Use only trusted credential configuration and endpoints.

{{example:model-vertex}}

## xAI

{{example:model-xai}}

## OpenRouter

{{example:model-openrouter}}

## DeepSeek

{{example:model-deepseek}}

## Local runtimes

Supply the runtime's `baseURL` and a model available on that runtime.

Ollama:

{{example:model-ollama}}

vLLM:

{{example:model-vllm}}

llama.cpp:

{{example:model-llamacpp}}

SGLang:

{{example:model-sglang}}

## Set a client or endpoint

For a compatible Responses endpoint, supply `baseURL`, `apiKey`, and `modelName`:

{{example:model-endpoint}}

Other adapters accept `models.Config`. Vertex AI has its own authentication configuration. Native
continuation belongs to the provider that created it. Do not change its provider label to force
incompatible data into another endpoint.

See [ReAct loop settings](/agent/react/) for call options and [Provider
support](/reference/providers/) for adapter differences. A shared interface does not imply identical
model capabilities.
