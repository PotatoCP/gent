# Encoding cost and conversation capacity

This follow-up keeps the reviewed saved format and recovery behavior. It reduces repeated work in
encoding, stream byte accounting, and temporary storage. It does not remove events or archive data.

## Changes and reasons

The codec registry now stores immutable descriptions of registered Go fields and scalar methods.
These descriptions contain no application values or custom-codec results. The encoder can reuse
field names, types, and callback rules without asking reflection to build them for every event.
Declaration names and checked derived-type names are cached only for one encoding operation. A
later operation can therefore see a new application registration without shared cache invalidation.

A bounded, per-operation cache now covers short built-in strings as well as long text. Run IDs,
message IDs, and empty fields recur often. Named scalar methods and application codecs keep their
normal call and error behavior. The cache does not keep application text after the operation.

The table writer builds arrays from typed values. It does not first box every child into `any`.
Container lengths set a bounded initial capacity before traversal. Larger values grow only as
child validation advances. Bounded scratch space holds child indexes. It never holds returned
bytes or application values after release. Large documents are not pooled.

Byte accounting and storage use the same exact table-size calculation. Shared-reference widths,
layout order, escaping, and compression selection remain unchanged. Storage allocates its output
buffer once. JSON scalar leaves need no separate tree index after `encoding/json` has validated
and normalized them. Object and array leaves still receive full depth and expansion checks.

Map decoding and Clone reuse assignment slots. Inserting a Go map entry copies the slots. Each
mutable child still gets independent storage. Nil values clear the slot, and duplicate-key checks
remain in place. Allocation limits are checked before allocation, as on the portable path.

## Verification of saved behavior

The pre-change source was saved before implementation. All configured provider and local checks
passed before edits. The earlier xAI browser failure was an HTTP 403 credit failure. After the
owner restored credits, the unchanged xAI browser passed. The failed log remains in the evidence.

A fixed mixed-value fixture produces the same complete component, plain document, compressed
document, and SaveBatch receipt as the pre-change source. It crosses the text-cache bound and
includes native JSON bytes, escaped Unicode, nil and empty values, arrays, and maps. The test also
checks decoded values and independent ownership. Existing exact byte-limit, malformed-data,
custom-codec, error, depth, map-key, archive, and savepoint tests remain unchanged.

An early candidate reserved capacity for a complete container before validating its children.
A 131,072-element slice with invalid zero-size records allocated 7.3 MB before its first error,
compared with 744 bytes at baseline. That measurement stopped the candidate benchmark. Staging
was restored. The final implementation bounds initial scratch capacity and uses about 58 KB for
that case. Tests cover large invalid slices, arrays, and maps, plus exact encoding beyond the
initial bound. The complete host comparison was then restarted; candidate samples are separate.

## Measurement method

The comparison uses the reviewed source before this optimization and the optimized source. This
is separate from the earlier comparison with v0.8.0. The parent of the v0.9.0 release commit keeps
the complete reviewed baseline. Both binaries use Go 1.24.10, the same benchmark fixtures, CPU
limits, and memory limits. All repeated samples are retained.

The host is a t3a.medium with two vCPUs and 4 GiB of RAM. It is the approved staging host. This is
not a measurement of the Intel t3.medium. Staging applications are stopped during measurement and
restored afterward. A separate absolute restart timer and systemd cleanup handler cover failure.

AWS lists a 20% CPU baseline per vCPU for both medium types: 0.4 vCPU in total. Unlimited mode can
use surplus credits above that allowance. The measured host had Unlimited mode and no banked
credits. The quota tests separate the included allowance from use of both vCPUs. See the
[AWS credit rules][aws].

[aws]: https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/burstable-credits-baseline-concepts.html

The business workload runs a real ReAct loop with managed tools, two fixed model responses,
100 answer chunks, and seven SaveBatch values per turn. It captures a RunStateSnapshot every
fourth save. Ten turns keep the full conversation without compaction. No remote provider or store
latency is included. Fixed responses isolate framework cost from changing model behavior.

Five alternating pairs measure persistence, replay, archive loading, and retained data. Three
alternating pairs measure CPU for 100 complete turns. CPU is process user plus system time. A
separate profiled run is excluded from the comparison. Byte counts can vary slightly across real
runs because IDs and times vary; the fixed-value test proves exact encoding equality.

Capacity checks hold 1,000 and 3,000 restored conversations in a managed stream wait. A separate
3,000-conversation test adds one Live and one Accepted Subscriber per run. It releases 290 replies
at a checked 0.4-vCPU allowance, then 600 at two vCPUs. All contexts remain resident. Delays measure
scheduled reply release to accepted answer. The ramp is a short workload, not a sustained soak.
Each waiting conversation has one completed business turn and waits in its next turn. The ten-turn
data measurement is separate. Longer retained context and larger custom state can use more memory.

## Host results

The complete-turn CPU median fell from 163.5 to 152.1 ms per turn, a 7.0% reduction. The three
baseline samples were 162.4, 163.5, and 164.9 ms; the optimized samples were 150.2, 152.1, and
156.4 ms. These samples include complete execution, persistence, and cleanup for 100 turns.

The following medians use five alternating pairs with three operations per sample. Allocation
bytes are temporary allocation totals, not retained heap or peak resident memory.

| Operation | Baseline time | Optimized time | Baseline bytes | Optimized bytes |
| --- | ---: | ---: | ---: | ---: |
| One-turn execution | 96.12 ms | 92.03 ms | 17,158,986 | 15,724,477 |
| Ten-turn execution | 1,320.15 ms | 1,213.59 ms | 236,892,976 | 215,236,208 |
| Full batch replay | 522.81 ms | 513.02 ms | 104,175,968 | 104,323,474 |
| State and tail restore | 38.92 ms | 38.54 ms | 7,983,552 | 7,824,813 |
| Archive reconstruction | 519.01 ms | 501.30 ms | 104,369,834 | 102,082,525 |

Ten-turn execution used 9.1% fewer allocated bytes and 16.1% fewer allocations. Its time ranges
were 1,310.75–1,528.06 ms and 1,180.03–1,251.41 ms. The other timing ranges overlap, so their small
median changes do not establish a speed improvement. Full replay allocated 0.14% more bytes;
that range also overlaps. No result is omitted because it was slower or larger.

Persisted bytes and retained state remain effectively unchanged in this follow-up:

| Ten-turn data | Baseline bytes | Optimized bytes |
| --- | ---: | ---: |
| All 70 SaveBatch documents | 736,154 | 736,138 |
| Encoded RunStateSnapshot | 13,381 | 13,377 |
| Encoded complete RunView | 95,101 | 95,029 |
| Decoded RunState heap | 98,064 | 98,032 |
| Decoded RunView heap | 1,836,912 | 1,836,944 |

The small differences above come from independently generated runs. They are not a new storage
reduction. Fixed values have exactly the same encoded bytes. The earlier storage compression
changes remain in place; this comparison starts after those changes and their review repairs.

## Capacity and limits

All 1,000- and 3,000-conversation wait tests passed. All 890 scheduled replies passed in each
resident subscriber test. Both versions ended with zero subscriber failures and zero remaining
workers. The full workload includes two subscribers per conversation and actual batch encoding.

| 3,000-conversation measurement | Baseline | Optimized |
| --- | ---: | ---: |
| Waiting heap, without subscribers | 108.0 MiB | 108.0 MiB |
| Peak process RSS, without subscribers | 399.7 MiB | 401.4 MiB |
| Waiting heap, with two subscribers | 119.9 MiB | 120.0 MiB |
| Waiting stacks, with two subscribers | 66.6 MiB | 66.3 MiB |
| Waiting goroutines, with two subscribers | 21,000 | 21,000 |
| Peak process RSS during subscriber load | 861.8 MiB | 858.9 MiB |
| Completion CPU at 0.4 vCPU | 141.3 ms | 129.4 ms |
| Completion CPU at two vCPUs | 142.7 ms | 129.8 ms |

The reply groups have four consecutive ten-second arrival phases. Each group drains before the
CPU allowance changes. Later phases can include a queue from earlier phases. These are complete
observed p95 delays from planned reply release to accepted answer, not independent steady loads:

| CPU allowance | Replies per second | Baseline p95 | Optimized p95 |
| --- | ---: | ---: | ---: |
| 0.4 vCPU | 1 | 546 ms | 317 ms |
| 0.4 vCPU | 4 | 50,819 ms | 36,350 ms |
| 0.4 vCPU | 8 | 81,892 ms | 73,240 ms |
| 0.4 vCPU | 16 | 77,263 ms | 68,123 ms |
| Two vCPUs | 4 | 172 ms | 96 ms |
| Two vCPUs | 8 | 243 ms | 307 ms |
| Two vCPUs | 16 | 9,326 ms | 1,580 ms |
| Two vCPUs | 32 | 19,645 ms | 15,874 ms |

Thousands of waiting conversations fit in this memory budget. Thousands of busy conversations
need a stated arrival rate. At the measured completion cost, 0.4 vCPU supplies about three
completions per second before headroom. Two vCPUs supply about fifteen. These are CPU estimates,
not safe service limits. The complete-turn measurement includes more work and gives lower limits.
The observed queues confirm that the included CPU allowance cannot sustain four replies per
second for this workload. At two vCPUs, eight replies per second had a low delay in the short test;
sixteen already formed a queue, and thirty-two formed a large queue.

If all 3,000 conversations need one complete turn each minute, the arrival rate is 50 turns per
second. At 152.1 ms of CPU per turn, that needs about 7.6 vCPUs before service headroom or external
work. One t3a.medium does not meet that load. Idle conversation count alone cannot establish
capacity. Provider and store latency, tool work, larger contexts, traffic bursts, and longer
operation must be measured for the actual service.

Keep full archives outside the active worker when they are not in use. Holding 3,000 decoded
archives of this ten-turn size alone needs about 5.1 GiB. The test keeps compact execution state
and discards bytes after simulated storage acknowledgement. It does not keep all archive bytes
in memory, model remote provider delays, or measure a production database.

All eight staging applications restarted and were checked again with unchanged process IDs.
A second cleanup pass initially tried to stop an already unloaded transient timer. The restart
had succeeded. The cleanup script was made safe to repeat, then passed twice. Its original error
log remains separate from the successful benchmark results. This was a measurement-controller
cleanup defect, not a library or benchmark assertion failure.

## Repeat the workload

Build the Linux fixture with `go test -c ./internal/footprint`. Set `GOMAXPROCS=2` and
`GOMEMLIMIT=2300MiB` for the host runs. Disable ordinary tests with `-test.run '^$'`.

- Persistence, replay, and retained data: select
  `Benchmark(BusinessPersistence|BusinessReplay|RetainedBusinessData)`, with `-test.benchtime=3x`.
  Run five alternating baseline/current pairs.
- Waiting conversations: select `BenchmarkWaitingBusinessRuns/1000` and `/3000`, each once.
- Complete-turn CPU: select `BenchmarkBusinessPersistence/turns_10` with `-test.benchtime=10x`.
  Run three alternating pairs and collect user plus system time. Keep a separate profile sample.
- Resident replies: select `BenchmarkResidentTurnRate` with `-test.benchtime=1x`,
  `GENT_BENCH_RUNS=3000`, and `GENT_BENCH_SECONDS=10`. Copy the same Linux fixture into the baseline
  checkout. The controller must set and verify CPU quotas before opening each gate.

The reply fixture uses `GENT_BENCH_GATE` for the first group and a `.burst` suffix for the second.
It writes a `.ready` file before each group. Open the corresponding gate only after checking the
cgroup's `cpu.max`: `40000 100000` first, then `200000 100000`. Without this controller, the phase
names do not apply CPU limits. Use `-test.timeout=1190s` for the full resident workload.

A 100-run, one-second-per-rate race check validates the fixture and worker cleanup. It is not a
capacity measurement. The full host tests use separate non-race binaries and verified quotas.

## Separate local stream measurement

Three alternating pairs process 1,000 stream chunks per operation, with 20 operations per sample.
These measurements use the local machine, not the staging host. They isolate stream handling from
the business workload. No other local test suite ran at the same time.

| Median per 1,000 chunks | Baseline | Optimized | Change |
| --- | ---: | ---: | ---: |
| Elapsed time | 142.65 ms | 136.27 ms | -4.5% |
| Process CPU time | 167.0 ms | 160.5 ms | -3.9% |
| Allocated bytes | 54,403,610 | 53,108,038 | -2.4% |
| Allocations | 961,571 | 865,659 | -10.0% |

Elapsed ranges were 142.47–145.45 ms and 133.82–139.35 ms. CPU and byte ranges overlap. These are
small repeated samples, not a guarantee for other models or content. Earlier candidate samples
are retained separately and are not used for this comparison.

## Completed verification

The full gate passed before edits and again after the final host measurements. The post-change
gate completed without a failed test or a retry. It ran these modes in the root, `docs`, `examples`,
`examples/chat`, and `scripts` modules, with `GOMAXPROCS=4` and a 300-second package deadline:

```sh
go test -p 1 ./... -count=1 -timeout 300s
CGO_ENABLED=0 go test -p 1 ./... -count=1 -timeout 300s
go test -p 1 -race ./... -count=1 -timeout 300s
```

All 15 module suites passed. The checks also passed all 22 independent regression probes,
both saved-document fuzz targets, six documentation tests, 81 chat UI tests, all 55 documentation
pages in each supported browser layout, the demo and native-response browser flows, package
contents, and CLI installation. Desktop and mobile screenshots were inspected.

Real provider calls passed for OpenAI, Anthropic, Gemini, Vertex AI, and xAI. They covered all
49 supported provider/group combinations, including NativeLive programmatic calls and search.
Image generation ran on OpenAI, Gemini, Vertex AI, and xAI; Anthropic has no image-generation test.
All eight live chat HTTP/SSE scenarios passed, as did xAI's additional streaming, airline, and
e-commerce checks. Full live browser flows passed separately on all four chat providers.

DeepSeek, OpenRouter, vLLM, Ollama, llama.cpp, and SGLang had no complete live configuration.
They are not reported as live-verified. No credentials or project configuration were changed.
Image model settings absent from the project were supplied only to the test processes.

The local evidence folders are `/tmp/gent-optimize-20261122` for the original baseline and
`/tmp/gent-optimize-20261123` for the resumed baseline, final host run, and post-change checks.
They retain commands, source hashes, binary hashes, every sample, profiles, screenshots, and the
stopped candidate run and timer-cleanup failure. Source history keeps the reviewed baseline separate
from the final optimization. No application value, accepted event, or recovery check was removed.
