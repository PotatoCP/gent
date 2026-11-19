# RunStateSnapshot size review

Status: draft proposals for owner review. No codec change is approved or implemented by this work.
The chat scenario and explorer changes are separate. Measurements use the current codec plus a
temporary, reversible format experiment. No production storage format was changed.

## Findings

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

Keep existing public storage calls for all stages. Table layout, sharing rules, and compression
remain draft until the owner approves the codec design. The explorer changes do not approve a
storage format change.
