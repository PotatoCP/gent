# Multi-turn implementation and verification

The implementation follows [req-multi-turn.md](../../req-multi-turn.md), through D143. The requirement
document remains the decision record. This report records implementation choices and verification;
it does not add public contracts or replace the requirements.

## Implementation map

| Contract | Implementation |
| --- | --- |
| Public execution methods | [executor.Runner](../../executor/executor.go) |
| Shared services and bounds | [Config](../../internal/core/config.go) |
| Admission, Stop, and Release | [attempt lifecycle](../../internal/core/context_attempt.go) |
| Accepted input and turn reset | [turn admission](../../internal/core/context_turn.go) |
| Compact execution replay | [RunState](../../internal/core/run_state.go) |
| Complete accepted archive | [RunView](../../internal/core/run_view.go) |
| State encoding and validation | [RunState codecs](../../internal/core/run_state_codec.go) |
| Stable lazy capture | [Recorder save path](../../internal/core/run.go) |
| Ordered bounded subscribers | [delivery worker](../../internal/core/subscriber_worker.go) |
| Native content and identities | [AssistantContent](../../internal/core/content.go) |
| Retained user input | [default policy](../../compaction/user_retention.go) |
| Generic summary and proposal acceptance | [summarizer](../../compaction/summarization.go) |
| UI projection of saved evidence | [trace replay](../../trace/replay.go) |

Root files expose the public types. The shared runtime lives in internal/core so executor.Runner can
bind that runtime without a second public construction or execution API. Aliases preserve type
identity; the private runtime does not require callers to import an internal package.

RunState keeps the active context and continuation evidence. RunView keeps the complete archive.
Absolute history and operation positions do not depend on retained slice lengths. Archive source
references remain valid when their records leave compact execution state. Capture must not pull all
referenced records back into memory.

Recorder owns the storage transaction. Gent owns coherent capture, encoding, replay validation, and
acceptance barriers. Every approved save contains the custom data and matching loop position. Both
replay paths must reach the same accepted logical state. A custom Next restarts at its entry point;
managed recovery preserves accepted results and completed recovery preparation.

## Verification coverage

The [savepoint matrix](../../internal/multiturntest/savepoint_matrix_test.go) covers 216 combinations:
ReAct or a custom loop without a step enum, nine save boundaries, four storage/interruption cases,
and three replay paths. It checks complete encoded state, custom data, counters, tool identities,
external effects, recovery preparation, continued output, and archive reconstruction.

Additional checks cover invalid and incompatible state, gaps and conflicting batches, exact retry,
overflow, ownership transfer, stable delayed capture, evolved configuration, subscriber failures,
bounded delivery, cancellation, repeated Release, rejected output, and accepted publication. Tests
use the public execution path where a task or a managed operation is required.

Review regression tests also check replacement recovery under exhausted turn and run limits,
cache-wrapped text before user retention, and value or pointer events with configured Hooks.
Blocked replacement keeps the old task and TurnId. Summary input covers the complete original text
before truncation, and the archive keeps that original. Hooks inspect only their supported events.

All eleven provider adapters have fixture tests for complete native requests, repeated rejection,
restored Continue, and restored NewTurn. These compare native phases, roles, grouping, signatures,
tool identities, and continuation fields. The [live multi-turn check][live-turn] uses a controlled
validator and both byte-restoration paths against configured remote providers.

[live-turn]: ../../models/provider_turn_live_test.go

Checks completed on 18 November 2026:

- Full native, CGO-disabled, and race suites passed with `-count=1 -timeout 300s`.
- The separate chat module passed native, CGO-disabled, race, and vet checks. Its client state
  tests cover accepted gaps, duplicate delivery, reused provisional positions, and source links.
- The Quick Start server passed native, CGO-disabled, and race suites.
- The tagged reliability probes passed with the race detector.
- Interface, lesson, and Quick Start browser checks passed. The streaming browser check includes
  refresh, byte-store recovery, native tool display, accepted-answer replacement, media, and mobile
  layout. Client reducer and connection tests also passed.
- OpenAI, Anthropic, Gemini, Vertex AI, and xAI passed the configured remote checks for native
  tools, output, history, compaction, cancellation, recovery, repeated rejection, and later turns.
- Ollama remains unverified: its model setting is empty and its configured endpoint refused a
  connection. DeepSeek, OpenRouter, vLLM, llama.cpp, and SGLang have no live configuration here.

Initial verification found that a recorder fixture copied the complete growing archive at every
save. It now decodes and transfers each new batch into its owned RunView. A later review race run
still reached the 300-second timeout. CPU profiles showed repeated JSON scans during state encoding
and decoding, and repeated verification of the same saved prefixes in the recovery matrix.

The codec now builds nested typed envelopes and serializes them together. It validates complete JSON
before decoding, then checks nested fields and types without repeating whole-subtree syntax scans.
Opaque values and custom codecs retain their validation and ownership contracts. A before/after
comparison checked identical bytes for nested values, maps, tasks, media, and opaque JSON. The codec
tests also compare Marshal with the complete Encode representation and check malformed nested data
before custom decode callbacks can run.

The matrix compares every accepted capture with full replay once. Each attempt still restores fresh
objects, compares the complete restored head with its saved capture, and checks custom data and
external work. All 216 cases and all saved-position comparisons remain. The timeout stays at 300
seconds. The full race command uses `-p 1` to bound concurrent package memory use.
The final full race run passed: internal/multiturntest took 228.108 seconds and models took 208.979
seconds. Both were below the unchanged 300-second timeout.

`go vet -structtag=false ./...` checks the remaining analyzers. The struct-tag analyzer reports two
deliberate ambiguous-JSON-field fixtures. Those fixtures test field selection and schema rejection;
removing the ambiguity would remove the behavior under test.

## State cost

The RunStateStorage benchmark separates capture, encoding, loading, tail replay, full execution
replay, and full archive replay. Measurements below use one operation per case on Linux amd64, AMD
Ryzen 7 5800HS with GOMAXPROCS=4, after the codec changes above. These measurements do not guarantee
latency. Allocations are total allocated bytes per operation, not peak resident memory.

| Operation | 100 iterations | 2,000 iterations | 2,000: allocated bytes |
| --- | ---: | ---: | ---: |
| Capture | 188 µs | 145 µs | 19,408 |
| Encode | 503 µs | 527 µs | 149,616 |
| Load | 2.46 ms | 1.86 ms | 228,864 |
| Load plus four later batches | 10.8 ms | 9.52 ms | 1,472,744 |
| Full RunState replay | 376 ms | 6.64 s | 1,016,325,560 |
| Full RunView replay | 325 ms | 5.98 s | 1,018,698,544 |

The captured state grew from 19,768 to 19,791 encoded bytes. Load plus the four-batch tail read
89,875 and 90,026 bytes. Full replay read 3,197,653 and 63,532,372 bytes. Capture allocated 329 and
336 objects; load allocated 1,686 and 1,679. The active state stayed fixed while the archive grew,
so capture and load did not repeat work over the earlier history.

Large active content still has a real cost. With a 1 MiB active payload, the capture used 19,408
allocated bytes because immutable strings can share storage. Encoding wrote 1,068,175 bytes and took
15.4 ms; loading took 119.2 ms and allocated 13,930,680 bytes. Loading plus four later batches took
305.7 ms. A compact execution state is bounded by retained data, not by a fixed byte constant.

Reproduce the benchmark with:

```sh
GOMAXPROCS=4 go test ./internal/core -run '^$' -bench '^BenchmarkRunStateStorage$' \
  -benchtime=1x -count=1 -timeout 300s
```

## Enum review

The Go AST inventory included public aliases, internal packages, provider adapters, fixtures,
examples, both auxiliary server modules, and generators. It reviewed 676 constant declarations,
including 370 declarations in 54 named typed groups. Enum values use named string types. Each value
has an immediately preceding comment that starts with its name and explains its use.

The inventory also reviewed untyped groups and numeric named types. Numeric limits, schema versions,
absolute identities, test data, terminal escape sequences, metadata keys, and open provider model
names are not closed sets of execution states. They retain their required representations. The ONNX
fixture generator maps its named string types to the protocol's numeric codes; its generated bytes
remain checked against the existing fixture.

JavaScript teaching states use documented string enum objects. TypeScript display values are
generated from the Go declarations with named string unions and per-value documentation. The
generator has a `--check` mode. DOM selectors, content identifiers, CSS classes, and data-driven
navigation keys remain data. Third-party Prism and dependency sources are outside Gent-owned code.

## Follow-up example

The [Conversation Lab](../../examples/chat/README.md) implements D81, D104, D132, and D143 after core
verification. It uses an in-memory key-value store of encoded batches and optional captures. Each
action restores a fresh worker from those bytes. It uses ordinary Config, Recorder, Subscribers,
Hooks, tools, and Runner methods. A process restart clears the store.

Deterministic tests verify replay equivalence, later-turn context, Stop/Continue/replacement,
completed-result inspection, storage failure barriers, exact retry and fencing, stale Stop, and
slow-browser disconnection without invalidating accepted execution. The browser keeps original
responses and processed accepted answers with source links. Native continuation data is retained
for replay and is not displayed as reasoning.

The demo browser and the configured live OpenAI browser both passed streaming, tool result display,
retained user input, same-RunId later turns, both replay paths, refresh, source links,
completed-turn
Continue, mobile layout, process lifetime, and browser error checks. The deterministic browser also
verified Stop, same-task Continue, and task replacement. Desktop and mobile screenshots were
inspected. The first browser run found a tool-result projection omission and an icon policy error;
both were fixed and the complete flow passed again.

The [release verifier](../../scripts/releasecheck/main.go) creates a real module ZIP with Go's
module packer from current tracked and non-ignored source. The inspected ZIP includes the root
module and excludes the chat module, its assets/dependencies, node_modules, and .env. The separate
go.mod supplies module exclusion; .gitattributes also marks the application for source-archive
exclusion. No release was created or published.
