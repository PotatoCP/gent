# Integration scenarios

Use the [streaming chat server](../examples/chat/README.md) to run the airline and e-commerce
scenarios interactively:

```sh
cd examples/chat
go run .
```

Select a provider from the credentials in the project `.env`. The browser shows model progress,
tool calls, accepted answers, and saved evidence. The chat list lets you return to an earlier
conversation. Server Reset clears conversations and their simulated service data.

The server uses the shared `NewChatConfig` functions in [airline](airline/chat.go) and
[ecommerce](ecommerce/chat.go). They supply the original tasks, prompts, rules, policy suggestions,
tools, and fixtures. The additional investigation tasks use the same data. The interactive terminal
menu and its conversation wrapper were removed to keep one interactive integration interface.
The separate model streaming menu was also removed. Its Go scenarios remain in
`models/streaming_test.go`, and their helpers now compile only for tests. The `gent` setup and
model-management commands remain available.

## Automated coverage

The existing Go tests still cover the scenario variants: native tools, SearchChain, policy
suggestions, compaction, and programmatic tool calling. They also keep provider request-mapping
checks and fixture assertions. Removing the terminal menu does not remove this coverage.

```sh
go test ./integrationtest/... -count=1 -timeout 300s
go -C examples/chat test ./... -count=1 -timeout 300s
node examples/chat/tests/browser.mjs
```

Without configured provider keys, live Go tests skip their paid calls. Fixture and browser demo
tests still run. The server module adds real HTTP/SSE tests for both investigation tasks with each
configured chat provider. It verifies tool work, progress, accepted output, and equivalent replay
from complete batches or a RunStateSnapshot plus its accepted tail.

Use the release environment helper to load one provider without executing `.env` as shell code:

```sh
go -C scripts build -o /tmp/gent-envrun ./envrun
/tmp/gent-envrun -env-file .env -provider OpenAI -- \
  go -C examples/chat test -run '^TestLiveChatScenarios$/^openai$/^airline$' -count=1 -timeout 300s
```

Use `ecommerce` for the other task. Provider names are `OpenAI`, `Anthropic`, `Gemini`, and `xAI`;
the matching Go subtest names are lowercase. These checks make paid calls. The release gate runs
both tasks for all configured chat providers. Live policy search also needs the ONNX setup and
`multilingual-e5-small` model described in the chat README.

`testutil.AvailableModels` retains the model configurations used by the automated scenario and
request tests. Change its settings with the associated request tests. The chat server has its own
small default model selection in `examples/chat/providers.go`; `_MODEL` settings can override it.
See [native model adapters](../docs/content/agent/models.md) for continuation behavior.
