# Gent

Gent is a Go framework for building LLM agents that can be adapted to your own agent loop, tool
selection, retrieval strategy, limits, and observability requirements.

Use the reference ReAct loop or write your own loop. Gent supplies typed tools, native model
adapters, streaming events, limits, and saved-state recovery.

## Philosophy

Gent is built around a few design choices.

- Library first. Your application owns the process, storage, models, tools, and deployment.
- Custom loops are normal. ReAct is included, but `Loop[T]` is the core abstraction.
- Tools stay typed. Tools receive typed Go input and return typed Go output.
- Use native APIs when they provide the requested behavior. Local output validation always runs.
- Tools use native API calls. A single contract covers direct tools, search, and JavaScript.
- Code stays simple. Remove unused code and interfaces instead of keeping compatibility paths.
- Limits are first-class. Iterations, tokens, tool calls, tool errors, and output rejections are
  tracked.
- Streaming is always on. Model chunks can be observed in real time.
- Retrieval is local-friendly. Optional ONNX embedding support runs on CPU.
- Observability is built in. Execution contexts publish lifecycle, model, tool, limit, and diff
  events.

Explicit state, limits, and events let your application inspect and control execution.

## Documentation

[Build and open the documentation site](docs/README.md). It contains short, tested examples and an
API reference generated from the current Go source. The site replaces the former Quick Start.

Start with [Hello World](docs/content/start/hello-world.md), then add
[tools](docs/content/agent/tools.md). The built site includes the code blocks in these source pages.

| Task | Guide |
| --- | --- |
| Select a provider or local endpoint | [Models](docs/content/agent/models.md) |
| Return typed, validated output | [Outputs](docs/content/agent/outputs.md) |
| Keep one run across user tasks | [Conversations](docs/content/run/conversations.md) |
| Save, restore, and continue work | [Recovery](docs/content/storage/restore.md) |
| Stream events to an application | [Subscribers](docs/content/observe/subscribers.md) |
| Search documents or tools | [Search](docs/content/search/documents.md) |
| Write a custom agent loop | [Custom loops](docs/content/extend/loops.md) |
| Use optional local embeddings | [Local embeddings](docs/content/search/embeddings.md) |

The current APIs are under development. Use the checkout and Go version specified in
[Setup](docs/content/start/setup.md). Do not assume that an earlier release has the same interfaces.

## Examples and verification

- [Hello World source](docs/examples/basics.go) and its [command](docs/examples/hello/main.go).
- [Small tested examples](docs/examples) for tools, outputs, search, compaction, and recovery.
- [History example](examples/history/main.go) for saved batches and restoration.
- [Conversation Lab](examples/chat/README.md) for a streaming chat application.

Conversation Lab stores encoded bytes in memory and is excluded from release source archives.
Its setup is separate from the teaching examples. The documentation's deterministic tests use local
fixtures. Real provider checks require configured credentials; see
[integration tests](integrationtest/README.md).

## Repository map

- Root Go files expose public interfaces and aliases.
- `internal/core/` implements shared state and execution services.
- `executor/` exposes the typed Runner.
- `agents/react/` contains the reference loop.
- `models/`, `toolchain/`, `output/`, and `compaction/` implement common interfaces.
- `search/`, `policy/`, and `common/` support retrieval and optional local models.
- `trace/` supplies display views; stored batches remain the recovery source.
- `cmd/gent/` supplies local runtime setup and model download commands.
- `docs/` contains the documentation site, its examples, and its checks.
- `development/` contains internal implementation and review records.

The [multi-turn requirements](req-multi-turn.md) retain the locked design decisions. Implementation
reports are in [development/reports](development/reports). Test rules remain in
[AGENTS.md](AGENTS.md).
