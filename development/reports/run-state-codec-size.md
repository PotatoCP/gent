# RunStateSnapshot size review

Status: the owner approved structural tables and local value sharing on 2026-11-19. The production
implementation is described in [Saved document format](saved-document-format.md). Compression is
still outside scope. The earlier sections below preserve the original measurements, design reasons,
and prototype evidence. They describe the codec before this implementation unless stated otherwise.

## Findings before the change

The inspector compares two formats of the same recovered state at the same accepted position:

- `CodecRegistry.MarshalRunState` produces a recoverable RunStateSnapshot. It stores concrete types,
  declared field types, versions, container information, and integrity metadata.
- `json.Marshal(state)` produces diagnostic JSON. It does not contain enough type and format
  information for the registry to restore the value. It is not an alternative save format.

The displayed sizes count compact UTF-8 bytes before HTTP encoding. Indentation in the browser and
HTTP string escaping do not explain the difference. These sizes do not measure Go heap use.

Relevant code:

- [Value encoding](../../internal/core/history_codec.go): `valueEncoder.encode` and
  `encodeContainer`.
- [Encoding envelopes](../../internal/core/history_codec_encode_result.go): `encodingValue`.
- [Type checks](../../internal/core/history_codec_decode.go): declared field and concrete type
  checks.
- [RunStateSnapshot codec](../../internal/core/run_state_codec.go): format, digest, and state
  validation.
- [Diagnostic JSON](../../internal/core/run_state_json.go): `RunState.MarshalJSON`.
- [Inspector comparison](../../examples/chat/inspect.go): fixed accepted head and exact byte counts.

A scalar field currently has an envelope similar to this:

```json
{"type":"/uint64","declaredType":"/uint64","version":1,"data":95}
```

Diagnostic JSON only needs `95` for the value. Struct fields repeat this metadata. Pointer, slice,
array, and map values add container envelopes. Struct fields use Go field names. The generic codec
also saves zero and nil fields that diagnostic JSON can omit through JSON tags.

The registry uses reflection to construct registered Go types and containers. The full name is a
stable registry key, not a package to import or code to execute. `resolveType` first looks up that
key in the registry. A type table can replace repeated names with document-local integer IDs and
resolve each ID through the same registry. Removing those checks is not necessary to save space.
The exact RunView name occurs only twice
in the measured archive. The broader repetition matters: that archive has 3,331 string envelopes
and 169 TextContent envelopes, as well as pointer, slice, and map envelopes.

RunView has a further difference: its codec saves a private compact replay state. The ordinary
RunView JSON shows only its public archive fields. That saved reducer lets a decoded RunView accept
later batches without rebuilding the archive. Thus the difference is not all text metadata. The
reducer repeats some archive content; local value references can reduce that repetition while
preserving its validation against the archive.

The metadata has a purpose. It preserves concrete values through interfaces, named types, number
widths, typed nils, container element types, and custom codec versions. Declared field types detect
incompatible code changes. Those checks must remain in a smaller format.

`json.RawMessage` uses a byte representation to preserve its exact contents. JSON then uses base64
for those bytes. Ordinary diagnostic JSON can rewrite that raw content. Media bytes also use base64.
Removing these byte representations without an exact replacement can damage native continuation
or integrity evidence. This overhead is separate from repeated type metadata.

## Measured sample

Date: 2026-11-18. The sample uses the chat example's deterministic airline provider, one customer
lookup, the shared airline request, and a second prompt that asks about the first request and
lookup.
It contains no media. The RunStateSnapshot is captured from the recovered final accepted state.

| Representation | Bytes | Relative to current saved bytes |
| --- | ---: | ---: |
| Current RunStateSnapshot | 59,467 | 100.0% |
| Diagnostic JSON, not a recovery format | 15,963 | 26.8% |
| Current bytes with Go gzip BestSpeed | 6,569 | 11.0% |
| Current bytes with Go gzip DefaultCompression | 4,652 | 7.8% |
| Current bytes with Go gzip BestCompression | 4,602 | 7.7% |

This is one size measurement, not a latency benchmark or a promised compression ratio. Different
savepoints, provider data, large custom data, and media can give different results. Encrypted or
already compressed content can compress poorly.

The sample contains 516 typed envelopes and 53 distinct type strings. A recursive JSON count gives
12,930 bytes for `type`, 12,844 for `declaredType`, and 6,192 for `version`, including each key,
value,
separator, and one trailing comma. These fields use about 54% of the saved bytes. This count does
not
include container element declarations or other container syntax. The outer format and digest add
only 104 bytes. Removing the digest would not solve the size problem.

The probe compressed the existing bytes at all three Go gzip levels, decompressed them, checked an
exact byte match, passed them through `UnmarshalRunState`, checked `RunStateReady`, and encoded the
loaded state again. The final encoding matched the original bytes. No recovery field was removed.
The temporary probe and sample are in `/tmp/gent-codec-airline-20261118/`; they are not product
code.
The existing replay tests also compare full batch replay with RunStateSnapshot plus its tail.

## New measurements: structural changes before compression

Owner direction on 2026-11-19: improve type/schema representation and repeated values before
compression. The exact format remains draft. Use document-local tables and keep each saved object
self-contained. The following measurements include the complete tables and format markers.

| Encoding stage, no compression | RunStateSnapshot bytes | RunView bytes |
| --- | ---: | ---: |
| Current codec | 59,467 | 760,167 |
| Type/declaration strings stored once | 53,797 | 697,136 |
| Also store record layouts and fixed metadata once | 29,984 | 219,581 |
| Also share repeated composite values | 24,871 | 95,987 |
| Reduction from current encoding | 58.2% | 87.4% |

The state column uses the prior airline sample. The archive column uses a fresh run of the same
two-turn airline scenario. Its companion state was 59,468 bytes and became 24,872 bytes. Each row
shows total file size, not just the value payload. The archive result is not a promise that its
ratio will remain constant as history grows.

The experiment uses a generic JSON record grammar to preserve all current metadata. Its first
stage uses explicit reference markers and escapes literal metadata values. A production encoder
that knows the field schema can use smaller integer slots. The measured first stage is deliberately
conservative; replacing names alone does not remove all the repeated envelope syntax.

The layout table removes repeated field names, declared types, and version fields from each value.
The final stage shares exact repeated composite values only when a reference and table entry use
fewer bytes. It does not shorten text, omit fields, prune history, or change compaction. The state
uses 105 dictionary strings, 163 layouts, and 22 shared values. The archive uses 154 strings,
268 layouts, and 129 shared values. These dictionary counts include declarations and container
types, not only distinct concrete Go types.

The prototype expanded both stages back to the exact original JSON bytes, including object key
order and number spelling. A separate Go probe passed the expanded bytes through the current codec.
RunState returned `RunStateReady`; RunView decoded successfully. Encoding the loaded values again
produced the same original bytes. This checks these samples, not the full future format contract.
The Go probe compared SHA-256 values to keep failure output small after exact expansion checks.

The source capture files were overwritten by another capture while the first Go check was pending.
That check correctly rejected the unequal samples. The retained expanded files still matched the
original measurement hashes. The final Go check used these fixed inputs and passed. The immutable
input hashes are:

- Prior state: `5154b89f17c395ffd506c20acd0c530b785c8ec71c3a2a43ad1e6311ae9baacb`.
- Fresh state: `8e17c136679bee320563149f5344970f167b9ddc4ab4385dd92ec233978589ba`.
- Fresh archive: `7289eefd90ca8e8acc031181f190d04939af402f9d745a2b3f67ac93b22e6be3`.

The repeatable measurement script is in
[development/experiments/codec-tables](../experiments/codec-tables/README.md).
Samples and Go probes are in `/tmp/gent-structural-codec-20261119/`. The `.frozen.json` inputs match
the hashes above. No production encoder or decoder uses the experimental format. No encode-time,
decode-time, allocation, or peak-memory improvement is claimed from a size result alone.

## First: type and record layout tables

Store type names, versions, declared field types, and record layouts once per document. Values use
small local indexes. Keep names in the document so a new process can resolve them against its
registry. Do not require a process-global ID allocation or an earlier file's dictionary.

This complete small example shows the measured record grammar. It represents two equal encoded
uint64 values. It is a syntax example; such a small document would not justify a shared table.

```json
{
  "format": "gent-table-draft-3",
  "types": ["/uint64"],
  "layouts": [
    [["type", {"type": 0}], ["version", {"constant": 1}], ["data"]]
  ],
  "shared": [[0, 95]],
  "root": ["array", ["ref", 0], ["ref", 0]]
}
```

The grammar is explicit:

- `types[0]` supplies the full type name. A name such as `github.com/rickchristie/gent/RunView`
  would also occur once in this table.
- `layouts[0]` supplies field order and fixed metadata. A one-item field declaration is a value
  slot. A two-item declaration supplies a constant or a type-table reference.
- `[0, 95]` uses layout zero and fills its data slot with `95`.
- `["ref", 0]` reads shared value zero. `["array", ...]` represents an original JSON array.
- Ordinary object layouts preserve their field names and order. Original strings and scalar
  values remain scalar data. Tags cannot collide with an application's original arrays or objects.

Both values expand to `{"type":"/uint64","version":1,"data":95}`. A declared field type is
another fixed layout field. Dynamic interface values retain their concrete type. Nil markers,
empty containers, byte strings, and opaque custom payloads are preserved by the same grammar.

For a production format, I recommend a typed schema table built by Gent's encoder, rather than
packing already-marshaled JSON. Statically typed fields can use their known schema directly;
interface values need an explicit concrete type index. Container element declarations belong in
the table. This can avoid creating the original large encoding before packing it. The prototype
proves structural size savings, not that its generic JSON grammar is the final wire design.

Preserve current compatibility checks. A version alone is not enough: current declared field checks
catch a changed Go field type even when a component did not increment its version. Keep field
names and declared types once in the schema. Preserve the existing behavior for harmless field
additions and unknown saved fields; a whole-type hash must not reject every harmless change.

Tradeoff: tables add decoder complexity and per-document setup. Small values can grow. A new format
needs deterministic indexing, an explicit wire version, and validation of every table reference.
Keep the public `MarshalRunState`, `UnmarshalRunState`, Marshal, and Unmarshal calls.

## Second: local references for equal saved values

Share exact repeated values within one document. This includes repeated outcomes, history content,
and the compact reducer stored with RunView. `RunState.LastOutcome` also occurs in its checkpoint;
the current validator requires equality. Store that value once and restore both logical fields
before running the same checks. Changing runtime state ownership is not required for wire savings.

Deduplicate the complete typed representation, not text alone. Type, version, field declarations,
native bytes, origin, and identities remain part of equality. Count dictionary and reference
costs before deciding to share. Long unique or already compressed data will save little.

Tradeoffs and constraints:

- References stay inside the file. A saved execution state must not need an old archive record or
  external blob to restore a required value.
- Decode each occurrence with the current ownership contract. Equal wire values must not silently
  become shared mutable Go maps, slices, or pointers. Sharing bytes is not sharing mutable objects.
- Reject missing references and cycles. Backward-only references can simplify cycle prevention.
- Bound expanded bytes, depth, and item counts, including repeated references. A small reference
  graph must not bypass existing decode limits or allocate an unbounded expanded value.
- Keep integrity checks over the new document's tables and root. Retain existing logical validation.
  The prototype expands to the old bytes for verification; a production decoder should decode the
  smaller format directly, without allocating a full expanded JSON intermediate.

This is wire deduplication. Removing duplicated runtime fields is a separate change that needs
its own proof. The size evidence supports a local table first, while keeping recovery validation
and ownership unchanged.

## Third: optional lossless compression

Evaluate this after the structural changes and their CPU and memory measurements. Compress the
complete validated encoding. Keep the public
`MarshalRunState` and `UnmarshalRunState` calls. Applications should still store and load bytes
through
Gent. Use an explicit format version and compression marker; do not guess the format from payload
data.

This changes storage representation without changing logical state. Decompression must produce the
exact original bytes before the existing digest, type, and logical-state checks run. The same
approach
can apply to complete SaveBatch values so archive storage benefits as well.

Required protections:

- Bound compressed input and expanded bytes. Reject excess expansion before allocating it in full.
- Keep depth, binary-size, type, version, integrity, and logical-state checks after decompression.
- Reject truncated streams, unknown formats, and invalid content through the existing typed results.
- Keep each RunStateSnapshot self-contained. Do not require an earlier dictionary or external blob.
- Keep batch receipts and accepted positions stable through a transport-only compression step.

Tradeoff: compression adds CPU work and temporary memory. Benchmark capture, save, load, and replay
latency before selecting a default level and minimum size. In this sample, maximum compression saves
only 50 bytes more than the default level. The size result alone does not establish the best level.
With the current encoder, compression after marshaling still requires the full uncompressed buffer.
It does not reduce the earlier encode/decode work or Go state size. A streamed encoding path can
reduce temporary buffers, but must keep exact digest coverage and size-limit checks.

A binary envelope avoids base64 expansion. A JSON envelope with a base64 compressed payload keeps
JSON tooling convenient, but adds about one third to the compressed payload. The inspector must show
the actual stored size and a separate readable projection if the codec becomes binary. HTTP
compression alone would reduce transfer size; it would not reduce stored bytes or codec allocations.

## Recovery constraints for any choice

Full batch replay must still equal RunStateSnapshot plus accepted tail at the same savepoint.
Preserve
custom LoopData, control and continuation state, counters, accepted answers, operation identities,
native provider bytes, pending tool plans, and completed recovery evidence. Never repeat completed
external operations to rebuild data that a smaller format omitted.

Keep the full accepted archive separately for RunView. Compaction may reduce execution state but
must
not delete the archived originals. Saving only diagnostic JSON, dropping required evidence, lowering
retention limits, or saving incomplete batches is not a codec size optimization.

Before approval, require:

1. Equivalence tests at every approved savepoint, including unfinished tools and recovered
  operations.
2. Typed nil, nil/empty containers, named types, exact integers, custom data, and native byte tests.
3. Corruption, truncation, unsupported version, changed field type, and expansion-limit tests.
4. Concurrent capture and save tests that preserve stable ownership and accepted cursors.
5. Size, CPU, allocation, and restore-time measurements with text, native state, media, and large
  data.

Keep existing public storage calls for all stages. The owner later approved structural tables and
sharing explicitly on 2026-11-19. That approval does not include compression. The explorer changes
remain separate from storage format approval.


## Production implementation and verification, 2026-11-19

The approved structural changes are implemented. The public storage calls remain unchanged. The
private format has a versioned integrity envelope, a type table, record layouts, and shared typed
values. It has no compression. The reader visits the compact tables directly and retains the
existing typed and logical-state validation. See [Saved document format](saved-document-format.md)
for the complete grammar, ownership rules, bounds, and unsupported former format.

### Exact sample comparison

The production encoder was also checked against the fixed input files from the earlier experiment.
The compact documents expanded to the exact original typed JSON bytes. The RunStateSnapshots loaded
as Ready, with the same logical state. The full archive loaded as RunView. This sample check is
separate from the full savepoint recovery tests below.

| Sample | Former bytes | Production bytes | Reduction |
| --- | ---: | ---: | ---: |
| Prior airline RunStateSnapshot | 59,467 | 27,131 | 54.4% |
| Fresh airline RunStateSnapshot | 59,468 | 27,132 | 54.4% |
| Fresh airline RunView | 760,167 | 80,782 | 89.4% |

The production grammar differs from the prototype. Its fixed format version and integrity envelope
are included in these sizes. Type metadata, complete native bytes, source identities, recovery
evidence, and original archived prompts remain present. No recovery field was removed.

### Encoding and decoding costs

`BenchmarkSavedDocument` constructs an accepted three-turn run through Runner and Recorder. It
compares the complete typed envelope with the document format on identical values. Both paths use
the current component codecs, including per-call reuse of repeated text and binary encoding. This
isolates the storage-format cost; it is not a comparison with a previous executable.

The following results are medians of three local samples, with GOMAXPROCS=4 and a 200 ms benchmark
target. Timing is approximate and can vary with machine load. Allocation bytes are total allocated
bytes per operation, not peak resident memory. These measurements do not prove a peak-memory gain.

The state case measures the retained state payload. It does not include the outer RunStateSnapshot
validation or Runner.Restore. RunView decoding includes its existing archive/reducer validation.
The native case preserves raw provider JSON; the media case includes 96 KiB of binary content; the
large-data case includes 736 KiB of application text. Long repeated data can benefit more than
unique or already compressed data.

In all tables, E means the complete typed envelope and D means the new document.

| Case | E bytes | D bytes | Reduction |
| --- | ---: | ---: | ---: |
| text state | 56,090 | 24,847 | 55.7% |
| text view | 247,953 | 35,786 | 85.6% |
| native state | 53,158 | 18,862 | 64.5% |
| native view | 261,864 | 30,884 | 88.2% |
| media state | 565,368 | 279,654 | 50.5% |
| media view | 2,030,428 | 290,629 | 85.7% |
| large data state | 2,314,029 | 772,400 | 66.6% |
| large data view | 9,305,353 | 784,349 | 91.6% |

Time, ms per operation:

| Case | Encode E / D | Decode E / D |
| --- | ---: | ---: |
| text state | 0.667 / 1.947 | 1.902 / 1.505 |
| text view | 2.892 / 5.210 | 8.772 / 4.508 |
| native state | 0.710 / 2.214 | 1.860 / 1.724 |
| native view | 3.406 / 6.831 | 9.287 / 4.961 |
| media state | 7.335 / 11.336 | 16.627 / 9.018 |
| media view | 19.444 / 16.510 | 54.343 / 23.570 |
| large data state | 17.539 / 24.525 | 50.660 / 23.872 |
| large data view | 71.679 / 26.605 | 201.651 / 77.973 |

Allocation, MiB per operation:

| Case | Encode E / D | Decode E / D |
| --- | ---: | ---: |
| text state | 0.196 / 0.617 | 0.316 / 0.519 |
| text view | 1.010 / 1.607 | 1.470 / 1.478 |
| native state | 0.235 / 0.747 | 0.369 / 0.577 |
| native view | 1.253 / 1.975 | 1.787 / 1.749 |
| media state | 1.215 / 2.131 | 1.582 / 1.295 |
| media view | 3.780 / 3.395 | 6.004 / 4.319 |
| large data state | 4.044 / 5.266 | 4.685 / 2.737 |
| large data view | 25.959 / 6.120 | 19.080 / 10.385 |

Allocation count per operation:

| Case | Encode E / D | Decode E / D |
| --- | ---: | ---: |
| text state | 1,851 / 7,286 | 5,367 / 7,034 |
| text view | 9,858 / 19,797 | 26,480 / 18,672 |
| native state | 2,439 / 8,443 | 6,984 / 8,100 |
| native view | 13,101 / 24,706 | 35,073 / 22,826 |
| media state | 1,871 / 7,429 | 5,424 / 7,158 |
| media view | 9,930 / 20,059 | 26,711 / 18,873 |
| large data state | 2,453 / 8,553 | 7,151 / 8,182 |
| large data view | 13,134 / 24,881 | 35,757 / 23,101 |

The cost is not uniform. Small state documents need more encoding time and allocation for tables.
Their decoder also allocates more bytes in these samples, despite lower elapsed time. Media and
large repeated archive data show better encoding and decoding results. Small documents can grow.
The selected design reduces stored repetition; it does not promise that every operation gets faster
or uses less memory. Separate receipt hashing still creates the full canonical typed JSON buffer.

Reproduce the format comparison with:

```sh
GOMAXPROCS=4 go test ./internal/core -run '^$' -bench '^BenchmarkSavedDocument$' \
  -benchmem -benchtime=200ms -count=3 -timeout 300s
```


### Review against req-multi-turn.md

The change was reviewed against the current requirements, including the owner's request to complete
this review before claiming the codec work is done. The review found no conflict in the affected
contracts. It does not reopen approved interfaces or claim a new provider feature.

- **D40/D52/D53/D62/D63/D65/D77, recovery equivalence:** both replay paths use the new document
  reader. `TestEverySavepointRestoresAcceptedState` covers all nine approved savepoint categories,
  ReAct and custom loops, four interruption positions, both replay paths, and exact retry.
  `TestRunStateSnapshotAndTailMatchFullReplay` compares complete encoded states. The codec
  does not change save timing, managed operation identities, accepted counters, or continuation.
- **D30/D78/D98/D118, compact state and full archive:** the serializer still receives the private
  retained RunState. `TestRunStateRetainsContextWithoutArchiveGrowth` checks bounded retained size.
  `TestRunViewReconstructsFullArchiveAndOldReceipts` checks complete
  history, activity, control state, and receipts, including applying more batches after a load.
  The wire tables do not remove source records or add references to another file.
- **D78, ownership and capture:** sharing occurs only in storage. New ownership tests decode shared
  pointers, maps, binary slices, and custom-codec buffers, change one occurrence, and compare the
  remaining complete values. Concurrent decodes remain independent.
  `TestRecorderRunStateSnapshotMatchesItsBatch` checks stable captures after later acceptance.
- **D46/D79/D112/D126, integrity and typed results:** digest, grammar, index, reference-order,
  logical-expansion, type, version, and state checks remain active. New tests reject corrupt and
  truncated data, unsupported formats, invalid references, and validly sealed invalid state.
  `TestRunStateLoadSeparatesMissingCodecAndOperationFailure` preserves the distinction between
  Incompatible and an unexpected codec error. Failed Unmarshal leaves its target unchanged.
- **D66/D105, accepted identities:** `TestSavedDocumentKeepsBatchReceipts` checks each recorded
  batch against the canonical typed representation. Storage table indexes and sharing do not change
  BatchId. EventSequenceNumber, RunId, TurnId, and operation identities retain their meanings.
- **D72/D76/D83, custom data and code evolution:** the existing evolution tests now also use
  complete document Marshal/Unmarshal. Added fields, removed fields with no remaining codec,
  renamed Go fields, declared-type changes, anonymous structs, and map-key collisions retain their
  prior rules. Component EncodedValue remains available for custom checkpoint fields. No migration
  framework or second public state type was added.
- **D73/D87/D98/D100/D103, output and native context:** provider restore/NewTurn and repeated
  rejection tests still exercise complete adapter requests after both replay paths. Exact native
  JSON bytes, signatures, phases, IDs, and ordered content remain saved. Accepted-answer publication
  still waits for its save. `TestAcceptedRecoverySurvivesAnotherInterruption` checks accepted
  recovery reuse. `TestRetainedUsersSurviveCompactionAndRestoration` checks compaction restoration.
- **D70/D75, enums and API simplicity:** both new internal enum families use named string types,
  typed constants, and per-value comments. Explicit conversion maps them to the private integer
  wire tags. The public storage calls are unchanged. The two added CodecLimits fields configure
  the expansion safeguards; zero selects bounded defaults.
- **D81, example integration:** the browser test uses the chat example's actual serialized memory
  store, both scenarios, both recovery paths, and the reusable RunView/RunState inspectors. It
  checks the encoded and recovered tabs and byte counts. The inspected Go module ZIP excludes the
  nested chat module and its assets.

The public compatibility boundary is explicit: this development format does not read former saved
storage envelopes. UnmarshalRunState reports Incompatible. It also rejects unsupported new document
versions. This follows the development policy that no backward compatibility or migration reader
is required. Logical state and batch receipt semantics are retained.

The review does not claim live provider compatibility. No provider mapping changed, and this task
uses provider fixtures and the deterministic chat provider. Configured live-provider checks remain
a release gate before a release or a new remote-compatibility claim.


### Capture, validated load, and replay

The existing `BenchmarkRunStateStorage` also passed with the new format. Each case creates an
accepted log through Recorder. The first two cases retain one 128-byte active input while history
grows from 100 to 2,000 iterations. The third retains 1 MiB of active text across two iterations.
Capture runs inside Recorder.Save. Load includes RunStateSnapshot integrity and logical validation.
It does not include Runner.Restore or later model/tool execution. Load plus tail reads up to four
subsequent batches. Full replay is measured for both RunState and RunView.

These are single-operation local samples, suitable for checking scale and byte counts. They are
not latency guarantees. Allocated bytes are total allocations, not peak memory. A dash means that
capture did not write or read serialized bytes.

| Case | Operation | ms | Allocated B | Allocations | Stored/read B |
| --- | --- | ---: | ---: | ---: | ---: |
| 100 | capture | 0.214 | 19,792 | 344 | — |
| 100 | encode | 1.575 | 571,856 | 6,111 | 15,806 |
| 100 | load | 1.067 | 439,984 | 5,787 | 15,806 |
| 100 | load + tail | 5.820 | 2,084,304 | 28,135 | 64,444 |
| 100 | state replay | 233.251 | 79,236,928 | 1,072,977 | 2,435,349 |
| 100 | archive replay | 212.419 | 79,458,504 | 1,073,261 | 2,435,349 |
| 2,000 | capture | 0.192 | 19,792 | 344 | — |
| 2,000 | encode | 1.500 | 570,592 | 6,099 | 15,822 |
| 2,000 | load | 1.532 | 441,232 | 5,799 | 15,822 |
| 2,000 | load + tail | 5.881 | 2,133,136 | 28,155 | 64,567 |
| 2,000 | state replay | 3,820.995 | 1,569,264,760 | 21,304,407 | 48,497,886 |
| 2,000 | archive replay | 4,115.802 | 1,571,550,464 | 21,308,478 | 48,497,886 |
| 1 MiB active | capture | 0.406 | 19,792 | 344 | — |
| 1 MiB active | encode | 38.706 | 11,064,088 | 6,113 | 1,064,220 |
| 1 MiB active | load | 26.599 | 2,538,288 | 5,799 | 1,064,220 |
| 1 MiB active | load + tail | 65.107 | 12,588,624 | 27,983 | 2,160,778 |
| 1 MiB active | state replay | 83.923 | 16,986,744 | 29,307 | 2,161,216 |
| 1 MiB active | archive replay | 63.724 | 8,461,624 | 29,278 | 2,161,216 |

Retained encoded size changes by only 16 bytes between 100 and 2,000 iterations. Capture allocation
remains 19,792 bytes and 344 allocations. Loading the retained state plus its tail reads about
64 KiB in both cases. Full replay reads about 2.4 MB and 48.5 MB respectively. Thus the new document
format preserves the retained-state loading path; it does not require earlier archive bytes.
Large active content still increases encoding and loading work, as the requirements permit.

Reproduce this check with:

```sh
GOMAXPROCS=4 go test ./internal/core -run '^$' -bench '^BenchmarkRunStateStorage$' \
  -benchmem -benchtime=1x -count=1 -timeout 300s
```

### Completed verification

All checks below passed on the final Go changes:

| Check | Result |
| --- | --- |
| Main module, native full suite | 24 test packages passed; all 31 packages reported |
| Main module, CGO-disabled full suite | 24 test packages passed; all 31 packages reported |
| Main module, race full suite | 24 test packages passed; all 31 packages reported |
| Chat module, native, CGO-disabled, and race suites | Passed |
| Chat JavaScript tests | All five files passed |
| Chat Playwright flow and console checks | Passed; screenshots inspected |
| Saved-document parser fuzzing | 297,523 executions in 15 seconds; passed |
| Format comparison and capture/replay benchmarks | Passed |
| Go module ZIP check | Chat module, assets, and .env excluded |
| Go format, whitespace, line length, Markdown links | Passed |

The Go suites used `-count=1 -timeout 300s`. Main-module suites also used `-p 1` and GOMAXPROCS=4.
The race suite exercised the complete savepoint matrix, provider fixtures, managed tool recovery,
concurrent capture/acceptance, subscribers, and shared-registry decoding. The browser flow used a
separate local fixture server and left the user's existing server running.

No test assertion was removed to obtain a pass. Binary wire tests now compare the document's exact
expanded typed representation with their original expected values. A small storage-limit fixture
was adjusted to include the new format header and still tests exact over-limit errors. Tests also
check exact table syntax, unchanged batch receipts, full decoded values, and no mutation on failure.
