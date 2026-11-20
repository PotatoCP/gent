# Saved document format

Status: implemented under the owner's 2026-11-19 approval of type tables, record layouts, and
document-local shared values. Compression is not included. This is the framework's storage format,
not an additional public state type or an application serialization task.

## Storage boundary

`CodecRegistry.Marshal` writes a versioned JSON document for SaveBatch, RunView, or a registered
value. `MarshalRunState` puts the same document inside the existing RunStateSnapshot integrity
envelope. Public storage calls are unchanged. Each document owns all its tables and references.

```json
{
  "formatVersion": 1,
  "data": {
    "types": ["/uint64"],
    "layouts": [
      [["type", {"type": 0}], ["version", {"constant": 1}], ["data"]]
    ],
    "shared": [],
    "root": [0, 95]
  },
  "digest": "SHA-256 in lowercase hexadecimal"
}
```

The example's digest is a label, not a valid digest. The real digest covers the fixed format marker
`gent-saved-document:1` followed by a newline, then the exact bytes of `data`. The outer object's
whitespace is not covered. Changing the spelling or whitespace inside `data` requires a new digest.
The format marker is checked before decoding. A digest provides integrity, not authentication.

`types` stores concrete names, declared field types, and container type declarations once. The
registry still resolves names. The document cannot load code or create an unregistered named type.
Registration is checked when a value is used, so an unknown saved field can be ignored after a
compatible code change without requiring its removed type's codec.

Each layout is an ordered list of field declarations:

- `["field"]` takes one value slot from the record.
- `["field", {"type": 0}]` takes the string at type-table index zero.
- `["field", {"constant": 1}]` takes a fixed scalar value.

The value grammar has no collision with original arrays or objects:

- `[layoutIndex, ...slots]` is a record. The slot count must match its layout.
- `[-1, ...items]` is an array.
- `[-2, sharedIndex]` is a reference to an earlier shared definition, or any definition at the root.
- `[-3, jsonValue]` is an opaque JSON leaf owned by a component codec.
- Strings, numbers, booleans, and null are literal scalars. Number tokens remain exact.

Custom JSON is opaque. Native `json.RawMessage` fields keep the existing exact byte representation;
they are not parsed and rewritten as provider instructions. Nil and empty containers, named types,
typed nil pointers, declared types, versions, and all content remain distinct.

## Construction and sharing

The encoder builds tables from the typed value tree before complete JSON serialization. Framework
codecs expose their typed children to that tree. Application codecs keep their JSON contract.
Each schema is prepared once per document. Struct fields and map entries retain deterministic order.
No global ID allocation or mutable table cache is shared between calls.

Repeated long strings and binary buffers reuse their encoded leaves within that call. This avoids
repeating JSON escaping or base64 conversion before the table builder compares equal typed values.
These temporary buffers do not escape into decoded state.

Equal typed nodes share an internal identity after a hash lookup and an exact comparison. Hash
equality alone is not sufficient. Wire sharing is selected only for a positive byte saving after
reference and table-entry costs. A conservative lower bound can leave a small saving unused. When
a parent is shared, its children are counted once for later sharing decisions. This prevents double
counting a saving. Definitions are emitted after their children, which makes references backward
only and prevents reference cycles.

The decoder indexes the stored JSON and builds a bounded view over the tables. Existing typed
decoders visit this view directly. They create new Go values for each logical occurrence. Thus an
outcome saved in two fields can use one wire definition while the loaded maps, slices, pointers,
and custom-codec buffers remain independent. No complete expanded JSON buffer is needed. A custom
decoder receives its own leaf buffer under its existing ownership contract.

Component `Encode`/`Decode` still use EncodedValue. They are not a second storage-document mode.
Batch receipt computation uses the complete typed component representation, with BatchId cleared.
Storage table IDs and sharing decisions do not affect an accepted receipt.
Receipt hashing still creates a complete canonical JSON buffer. This change removes that buffer
from storage encoding and decoding; it does not remove the separate receipt-hashing cost.

## Validation and limits

The reader rejects duplicate or missing document fields, invalid tags, duplicate layout fields,
wrong slot counts, invalid type indexes, and missing or forward shared references. It checks all
shared definitions before it runs a custom decoder. It then applies the existing typed and logical
state checks. RunState load retains Ready, Invalid, and Incompatible results. An unexpected custom
codec failure retains its wrapped error. Unmarshal does not change its target on failure.

The following bounds apply before typed construction:

- MaxEncodedBytes bounds the stored JSON, including the RunStateSnapshot envelope.
- MaxExpandedBytes counts the complete logical JSON, including every reference use. Its zero default
  is 256 MiB.
- MaxExpandedValues counts logical JSON values, including repeated references and unknown fields.
  Its zero default is 4,194,304.
- Expanded document depth cannot exceed 512. The existing typed-value depth bound is still 100.
- MaxBinaryBytes keeps the existing per-buffer binary bound.

Positive expansion limits replace the defaults. Encoded and binary limits remain unset at zero.
The expansion accounting uses checked addition before allocation; a small reference graph cannot
request exponential Go values or bypass a limit through an ignored field. Custom codecs still own
their internal allocation limits. These bounds measure the representation, not exact Go heap use.

This development change does not add a reader for the former storage format. It preserves the
component envelope so custom checkpoint APIs and accepted receipt semantics remain unchanged.
RunState load reports Incompatible for the former format and unsupported document versions.

## Requirements review and checks

The final verification report is recorded in
[RunStateSnapshot size review](run-state-codec-size.md). The review must check the current
[multi-turn requirements](../../req-multi-turn.md), including the recovery and archive invariants,
ownership, custom data, typed results, savepoint coverage, native content, and concurrent use.

Run the repeatable cost comparison with:

```sh
go test ./internal/core -run '^$' -bench '^BenchmarkSavedDocument$' -benchmem -count=3 -timeout 300s
```

It compares the former complete typed envelope and the new document on the same accepted
three-turn run. Cases include text, native JSON, media, and large application data. State and
archive are measured separately. Existing benchmarks measure capture, state validation, and replay;
the format benchmark must not be presented as total restoration latency.
