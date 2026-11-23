# Persistence and execution footprint

The owner requested this work on 2026-11-20. Keep all features and behavior while reducing persisted
SaveBatch bytes, delivery bytes, RunState and RunView costs, and the cost of running and restoring
agent loops. The capacity target is thousands of concurrent business conversations on one t3.medium.

## Constraints

- Preserve complete accepted events, original prompts, tool results, native data, and custom state.
- Preserve the full-replay and RunStateSnapshot-plus-tail recovery invariant at every savepoint.
- Preserve exact receipts, atomic acceptance, ownership, errors, cancellation, and size limits.
- Keep all evidence, compaction policy, and separate stream events.
- Keep custom codecs and all five Go modules working. Preserve archive inspection and the chat UI.
- Measure bytes, CPU, allocated bytes, retained heap, goroutines, and restore cost separately.
- Do not claim EC2 capacity from a local microbenchmark or a count of idle handles.

## Capacity model

[AWS](https://aws.amazon.com/ec2/instance-types/t3/) lists two vCPUs and 4 GiB for t3.medium.
Its CPU baseline is 20% per vCPU, with 24 credits earned per hour. Measure both sustained baseline
work and bursts. Remote model and tool work must remain separate from framework CPU work.

Exercise waiting conversations, active streaming, tool use, multiple turns, compacted context, full
archive loading, and restore bursts. Include small records, native state, large unique data,
and media. Record workload size and request rate with every capacity result. A local two-processor
test does not reproduce EC2 CPU speed, storage, network, credit accounting, or provider latency.

## Work sequence

1. Capture a baseline and profiles from the current implementation and realistic public-API flows.
2. Remove repeated encoding and temporary allocations without changing saved logical values.
3. Reduce stored and delivered bytes; measure any CPU and memory cost of the representation.
4. Reduce retained memory and restore work without sharing caller-owned mutable values.
5. Run concurrency and recovery checks, then all native, portable, and race suites in all modules.
6. Record before/after results, verified capacity, and remaining limits.

This report records the implementation and measurements under the goal. It does not add a service
capacity guarantee. The implementation passed the verification listed below.

## Baseline

Baseline revision: `7a419d7e7356a790dc5824ff2be72010031475eb`. The checkout was clean when this work
started. Local artifacts are in `/tmp/gent-footprint-20261120`. Benchmarks use `GOMAXPROCS=2`.
The final measurements below include the later correctness fixes. Their test and measurement logs
are in `/tmp/gent-fix-20261120-round1`.

The current storage format already uses document-local type, layout, and shared-value tables. It
preserves independent mutable values after decode. Keep these guarantees. The earlier format work
and its tradeoffs are recorded in [Saved document format](reports/saved-document-format.md).

`BenchmarkRecordAnswerStream` records 1,000 four-character chunks through real execution and
acceptance. Three baseline samples take 214–219 ms and allocate 116–118 MB, with about 1.38 million
allocations per run. This is allocated memory over the operation, not retained heap.

The profile identifies per-event document construction as a large cost. `queueEvent` calls Marshal
to enforce encoded-byte bounds; SaveBatch receipt hashing and storage perform more encoding later.
The table builder's node interning and layout construction account for much of the temporary memory.
The changes target that repeated work. Exact event content and size-limit behavior remain required.

## Implementation and reasons

The storage and recovery APIs remain the same. `Marshal` and `MarshalRunState` now select lossless
Snappy compression when table data is at least 8 KiB and the complete JSON document is at least one
eighth smaller. This includes base64 overhead. Small and incompressible values keep plain tables.
Use `WithCodecCompression(CodecCompressionNone)` for directly readable tables. Each document remains
self-contained, with no shared process dictionary or dependency on earlier files.

Fast gzip saved more bytes in the initial experiment, but used about five times the compression CPU
for that batch set. Snappy fits the CPU constraint better. The format remains JSON so existing
storage, transport, and inspection paths can carry it. The reader checks integrity and decompressed
size before allocating tables, then checks logical expansion before constructing typed values.

Other changes remove work or retained allocations at their source:

- Build layouts as typed fields, write validated JSON directly, and reuse bounded scratch indexes.
  Clear all application references before reuse. Large encodings do not set the idle scratch cost.
- Use a fast process-local hash to find equal nodes, with full equality checks for collisions.
  Integrity and batch receipts still use SHA-256. Internal hashes never become saved identities.
- Count uncompressed event bytes without creating the final buffer or digest. Keep the existing
  pending-event and Subscriber allowances independent of compression. A smaller storage file must
  not allow a larger live queue. Application codec failures and expansion checks still apply.
- Share immutable built-in type descriptions. Keep custom registrations, limits, and bound codecs
  local. Read fixed bindings without taking the local registry lock for every scalar value.
- Decode long shared built-in strings once per read. Named scalars keep their JSON and text decode
  methods, including validation. Slices, maps, pointers, bytes, and custom-codec buffers remain
  independently owned. No decoded value borrows input bytes.
- Check derived array sizes and reflective allocations before construction, with checked arithmetic.
  A small type declaration can otherwise exceed the address space despite the logical JSON bound.
  Writers use the same allocation bound. Nil and empty stream source-ID slices remain distinct.
- Allocate stream queues on the first Send. A waiting stream no longer retains 64 unused slots.

The storage changes preserve batch receipt identity, save acceptance, event order, original prompts,
tools, native evidence, custom state, output validation, and compaction selection. The correctness
work also restores structured instructions and media in the summary request as read-only context.
The saved request records this input. Compression does not alter UI event schemas. A transport that
forwards the stored bytes receives the same byte reduction; its own JSON projection or HTTP
compression is a separate cost.

## Workload and measurements

The final business fixture has distinct purchase-order lines, quantities, prices, suppliers, and
delivery dates. It uses the public ReAct Runner, one managed lookup per turn, two model responses,
100 answer chunks, seven saves per turn, and a RunStateSnapshot every fourth save. The ten-turn
case retains the conversation without compaction. The latest periodic RunStateSnapshot has one
accepted tail batch. It preserves all activity in the archive.

This is a synthetic procurement workload with real framework execution. It does not call a remote
provider or database. The same fixture runs on the baseline revision and the changed library.
The earlier repeated-text experiment remains in the local logs; final results use distinct data.

Local measurements use two Go processors on an AMD Ryzen 7 5800HS. Table values are rounded medians
from three samples. KB and MB here use decimal bytes. Allocated bytes measure work over time; live
heap measures retained objects after garbage collection. Neither value alone is process RSS.

| Ten-turn value | Baseline | Changed | Reduction |
| --- | ---: | ---: | ---: |
| Complete batch archive, 70 saves | 2.30 MB | 0.737 MB | 68% |
| Latest periodic RunStateSnapshot | 47.3 KB | 13.4 KB | 72% |
| Serialized RunView | 382.6 KB | 95.0 KB | 75% |
| Decoded periodic RunState, live heap | 190.1 KB | 98.1 KB | 48% |
| Decoded RunView, live heap | 3.12 MB | 1.84 MB | 41% |

A fresh context with no task fell from about 46.6 KB to 4.3 KB of live heap. The 1,000-chunk test
reduced temporary allocation from about 117 MB to 56 MB. These measure different lifecycle states;
do not use the fresh-context size as the memory estimate for an active business conversation.

The complete ten-turn execution fell from 726 ms to 493 ms and from 350 MB to 236 MB of temporary
allocation. The 1,000-chunk execution fell from 215 ms to 119 ms. These are framework workloads with
fixed local responses, not end-to-end model latency.

There is a read-cost tradeoff. Full batch replay rose from 197 ms to 205 ms in the local in-memory
test. RunStateSnapshot plus tail replay and Restore changed from 16.1 ms to 15.0 ms. Compression
reduces the bytes that a real store must read, but this benchmark excludes storage latency. Do not
describe all replay paths as faster. The common checkpoint path keeps its low restore cost.

The 100-turn timing run used 4.44 CPU seconds, or about 44 ms per turn, including setup and test
process overhead. It completed about 28 turns per second locally. The t3.medium baseline provides
0.4 CPU-seconds per second across both vCPUs. Dividing by the local cost gives about nine turns per
second before any CPU reserve or unmeasured service work. This is a planning estimate, not an EC2
measurement. A 50% reserve would reduce that estimate to about four or five turns per second.

The final capacity test held 3,000 distinct restored runs in their next turn, each waiting on a
managed model stream. It used two Go processors and a 3 GiB soft Go memory limit:

| Capacity measurement | Result |
| --- | ---: |
| Waiting restored runs | 3,000 |
| Retained heap per run | 37.7 KB |
| Stack allocation per run | 18.1 KB |
| Goroutines per run | 5 |
| Peak process RSS, including startup | 406 MiB |
| Remaining workers after cancellation and Release | 0 |

The full test took 150 seconds, including creation of saved business turns, restoration, startup,
measurement, and cleanup. This is not a restore-only timing. It recorded no swaps. The process
includes normal SaveBatch encoding but no database, network sockets, or remote provider requests.
The result supports thousands of waiting framework runs within the memory target. It does not prove
that thousands of conversations can all produce output at once on a t3.medium.

## Verification

All 15 required Go suites passed: native, CGO-disabled, and race in the root, docs, examples,
examples/chat, and scripts modules. Each used `-count=1 -timeout 300s`. The model package and the
savepoint recovery suite passed under the race detector. No Go suite needed a retry.

The table-format and compressed-format fuzz checks passed. Tests cover exact byte counts, codec
isolation, mutable ownership, native bytes, malformed compressed data, integrity checks, expansion
bounds, and full replay equivalence with RunStateSnapshot plus tail replay. The business recovery
test restores both storage modes and starts the next turn with the previous answer retained.
The later checks cover named-scalar decode methods and failures, nested type and allocation bounds,
nil/empty stream source IDs, and the complete summary request with native system-prefix attachments.
The summary check also compares the saved request evidence and the unchanged original prefix.

Documentation tests and the site build passed. Browser checks passed for all 55 pages from local
files and both HTTP base paths. The chat's 58 Node tests and demo browser flow passed, including
streaming, retained input, both replay paths, byte sizes, and encoded/recovered state inspection.
These checks used fixture providers. No live provider or actual EC2 load test was run.
The final documentation check needed local child-process access after the sandbox returned
`spawnSync go EPERM`. The same check passed with that access; no test assertion was changed.

## Repeat the checks

The sources are in `internal/footprint/` and the codec and stream benchmarks in `internal/core/`.
Initial and baseline logs are in `/tmp/gent-footprint-20261120`. Final logs after the correctness
fixes are in `/tmp/gent-fix-20261120-round1`. The baseline copy is an archive of the initial commit
with the same workload files added. No baseline implementation files were changed.

```sh
GOMAXPROCS=2 go test ./internal/footprint -run '^$' \
  -bench 'Benchmark(BusinessPersistence|BusinessReplay|RetainedBusinessData)$' \
  -benchmem -benchtime=200ms -count=3 -timeout 300s

go test ./internal/footprint -c -o /tmp/gent-footprint.test
GOMAXPROCS=2 GOMEMLIMIT=3GiB /usr/bin/time -v /tmp/gent-footprint.test \
  -test.run '^$' -test.bench '^BenchmarkWaitingBusinessRuns/3000$' \
  -test.benchtime=1x -test.timeout 300s
```

The capacity test creates distinct runs, completes a business turn, restores from the periodic
RunStateSnapshot plus its tail, then starts the next turn and waits on its model stream. It includes
normal save encoding and managed workers. A remote-store substitute discards acknowledged bytes,
so this test does not retain the database archive in the worker. Cancellation and Release must join
the workers. Startup includes creation of the saved fixture and is not a restore-only timing.

## Capacity limits

Thousands of waiting runs are different from thousands of streams producing chunks at once. The
active turn rate, chunk rate, context size, media, custom data, subscribers, storage latency, and
capture cadence determine service capacity. An actual t3.medium test is still needed for EC2 CPU
speed, network connections, provider traffic, and CPU-credit behavior.

Complete RunView history still grows with accepted activity. Loading 3,000 ten-turn RunViews at once
would exceed 4 GiB with this workload. Keep archived bytes in storage and load full views for active
inspection. Execution uses compact RunState and does not need to keep every archive in memory.

Remaining costs include canonical receipt hashing, typed value construction, frozen event copies,
and provider/application work. A very large active context or custom LoopData can dominate the
worker. Neither compression nor a RunStateSnapshot removes those logical values. No evidence or
feature was dropped to obtain the reductions.
