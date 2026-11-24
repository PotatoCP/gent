This table describes Gent's current adapter paths. It is not a promise that every model at an
endpoint supports every control. Choose the model and run the configured provider checks.

| Adapter | Request family |
| --- | --- |
| OpenAI, xAI | Responses |
| Anthropic | Messages |
| Gemini | GenerateContent |
| Vertex AI | GenerateContent with project and OAuth authentication |
| OpenRouter, DeepSeek | Chat completions |
| vLLM, llama.cpp, SGLang | Configured chat-completions endpoint |
| Ollama | Native chat |

## Adapter feature paths

This table describes the checked-in mapping code. A remote model can have narrower limits.

| Feature | Current Gent mapping |
| --- | --- |
| Native tools | All eleven adapters map native requests and correlated replies |
| JSON output | Native controls follow the adapter's schema rules; local checks always run |
| Image input | Image parts are mapped; DeepSeek uses unsupported-media guidance |
| PDF input | OpenAI, Anthropic, Gemini, Vertex AI, and supported OpenRouter routes |
| Audio and video input | Gemini and Vertex AI use an explicit MIME list |
| Image output | OpenAI, xAI, Gemini, Vertex AI, and supported OpenRouter routes |
| Audio output | The current adapters reject this requested output modality |
| Reasoning | Readable reasoning stays separate from opaque continuation evidence |
| Continuation | Native IDs, grouping, phases, and signatures remain provider-specific |
| Cache hints | Anthropic maps block cache hints; other native controls depend on the adapter |

The [capability fixtures](/source/models/provider_capability_test.go.html),
[tool and JSON fixtures](/source/models/provider_tool_json_test.go.html), and
[image fixtures](/source/models/provider_image_test.go.html) check these adapter paths.
The [restoration fixtures](/source/models/provider_resume_history_test.go.html) check native replay.

When one assistant message requests several tools, Gemini, Vertex AI, and Anthropic receive the
results together in the original call order. Gent keeps each tool result in the run history.
OpenAI Responses receives one result item per call. Chat Completions receives one tool message
per call.

The xAI image path requires JPEG output. Do not use the PNG-only setting from the image-limit
example with that adapter. Gemini and Vertex AI use their own allowed MIME list. OpenRouter checks
its model metadata before applying supported output controls. See the [Gemini media
mapping][gemini-media] and [chat media mapping][chat-media] for the exact local rules.

[gemini-media]: /source/models/gemini_messages.go.html
[chat-media]: /source/models/chat_messages.go.html

## Check the selected feature

- Native tools: preserve request IDs and correlated results.
- Structured output: use native controls where supported, then run local validation.
- Media: check the provider and model for each MIME type and input/output direction.
- Reasoning: show exposed content only; retain opaque continuation separately.
- Continuation: retain phases, signatures, and required native groups across later turns.
- Cache controls: use supported hints without promising a cache hit.

## Verification scope

The example tests use deterministic and local HTTP fixtures. The library's provider fixture suite
checks native requests and saved-state mappings. These pages do not claim current remote support for
an untested model. Live checks require configured credentials and must report unavailable providers
rather than silently skipping them.

[Models](/agent/models/) contains the constructor examples and endpoint exceptions.
