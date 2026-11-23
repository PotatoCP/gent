# Saved document format

Status: type tables, record layouts, and document-local shared values were implemented under the
owner's 2026-11-19 approval. The footprint goal adds lossless compression, enabled by default
for useful savings. This is the framework's storage format, not another public state type.

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

Format version 2 stores the same `data` object as a Snappy block in a canonical base64 JSON string.
Its digest uses `gent-saved-document:2`, a newline, and the exact quoted `data` bytes. There is no
dictionary or dependency on an earlier document. Both versions are supported; the run schema and
batch receipts are unchanged. A RunStateSnapshot still has its outer integrity envelope.

The default writer tries compression for table data of at least 8 KiB and keeps it only when the
quoted base64 result is at least one eighth smaller. Count base64 overhead in this decision.
Already compressed media must not grow in storage. Use
`WithCodecCompression(CodecCompressionNone)` for directly readable tables. Snappy was selected over
fast gzip because the business workload showed lower CPU cost. The extra gzip saving did not justify
its extra CPU for many active conversations. See the footprint report for measurements.

`types` stores concrete names, declared field types, and container type declarations once. The
registry still resolves names. The document cannot load code or create an unregistered named type.
Registration is checked when a value is used, so an unknown saved field can be ignored after a
compatible code change without requiring its removed type's codec.

Codec names must contain valid UTF-8. JSON replaces malformed bytes with U+FFFD, which can make a
registered type unreadable or select a different binding after storage. Both registration APIs
reject such names before changing either map. Valid Unicode names, including U+FFFD, remain valid;
do not normalize or replace bytes to repair an invalid registration.

Application registrations cannot use the decoder's value tags or type aliases: `bytes`, `nil`,
`pointer`, `slice`, `array`, `map`, `time`, `error`, `encoded-value`, `raw-json-bytes`,
`raw-json-nil`, `any`, `content`, and `error-interface`. They also cannot start with `*`, `[`,
or `map:`.
Reserve these full prefixes, including incomplete syntax, so a name cannot replace a derived type
description. Both registration APIs check this rule before changing either registry map. Checking
only for duplicate map entries is not sufficient: these reserved names have no ordinary bindings.
A rejected registration must leave existing values readable and allow a valid name for that type.

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
No global ID allocation is used. Bounded scratch indexes can be reused only after all application
references are cleared. A large encode must not set the retained scratch cost of later small events.

Map entries sort by the saved key type and data. Distinct pointer keys can have equal saved values,
including pointers inside struct, array, and interface keys. Tied entries sort by their complete
typed value bytes. All entries remain present, even when both saved key and value are equal. Pointer
addresses and Go map iteration cannot define storage order because neither survives decoding.
The value comparison only encodes tied groups. Ordinary distinct keys keep their existing receipts
and avoid this extra work. This rule applies before both component and storage encoding.

Zero-size pointer targets need nonzero backing storage when decoded or cloned. Go can otherwise
place separate empty targets at the same address and collapse map keys. Allocate a struct with the
target first and a trailing byte, then return the target's address. The pointer keeps its backing
storage alive. Check that same allocation on write and read. This adds no wire fields or address
identities and does not weaken duplicate-key validation.

Ordinary strings must contain valid UTF-8. Reject malformed bytes before encoding them, including
long cached strings, nested fields, and map keys. JSON replaces malformed bytes with U+FFFD; an
integrity digest computed after that conversion cannot detect lost content or combined keys.
An actual U+FFFD character is valid. Application codecs and explicit scalar JSON or text methods
retain their own format contract. Binary values can use byte slices. Clone uses the same value rule.
Compact framework wire structs must apply this rule too. Their direct JSON fields include saved
error messages and codes, stream source IDs, and component type names. These fields bypass the
ordinary scalar encoder and need validation before their compact representation is built.

Built-in time encoding keeps a JSON timestamp and offset, not location identity or monotonic data.
Reject maps that depend on these omitted distinctions to keep keys separate. Inspect times inside
comparable struct, array, and interface keys. Normalize those times in a local comparison value;
compare other fields with Go key equality, including equal positive and negative zero. Reuse a
normalized time for each saved timestamp so time parser allocation cannot decide map support.
A non-nil framework pointer keeps each decoded occurrence distinct, including zero-size targets.
Application codecs own their private format and are not decoded merely to validate a map write.
Include typed nil errors and timestamps in the same comparison value. A key can become equal only
after both conversions. Default typed nil errors become nil in an interface. Include unchanged nil
interfaces in the comparison too. An explicit codec can retain its typed nil pointer and takes
precedence. Non-nil default errors decode to independently owned SavedError pointers. These keep
entries distinct. Preserve the reader's duplicate-key check and reject collisions before returning
bytes.
For roots and interface destinations, encode a nil error with the same nil envelope used after
loading. Concrete pointer destinations retain their declared type and existing encoding. This keeps
receipts stable for supported maps with one typed nil error key.
Ordinary scalar keys avoid this normalization work. Clone uses the same map validation before it
returns a normalized value. Reject before checkpoint replacement or batch acceptance, so previously
accepted state remains readable and can Continue without a repeated task submission.

Default error encoding produces `*SavedError`, including for reflection-registered error types.
Carry each field, element, map key, and pointer target's declared type through encoding. Before
writing a non-nil error, check that the destination can receive `*SavedError`. Root components have
no declared destination. Error and any interfaces accept the default representation; a concrete
error destination can require an explicit application ValueCodec. Nil errors decode as the
destination's zero value, so readable nil pointers and empty containers remain supported.
This check belongs at the normalization step. It needs no second traversal, document decode, or
application Decode call during writing. Custom codecs keep precedence and their exact-type return
contract. Reject a mismatch before replacing custom state or accepting a batch. Clone uses the same
portable check if its normalized child cannot fit. The previous accepted task can then Continue
after restoration without resubmission. RegisterType alone cannot opt out of error normalization.

Clone can directly copy an immutable scalar only when JSON/text methods cannot change its portable
value or validation. Detect value-receiver marshal methods and pointer-receiver unmarshal methods.
The scalar writer passes a value to json.Marshal, so a pointer-only marshal method does not apply.
Use the portable path before invoking applicable methods. This preserves JSON precedence, complete
field diagnostics, and errors.Is with or without an encoded-byte limit that permits the value.
Explicit ValueCodec bindings keep precedence. Ordinary scalars keep direct copies. Frozen event
data from Accepted/Live delivery and accepted archive data must contain the same logical value.

Repeated built-in strings and binary buffers reuse their encoded leaves within that call. A bounded
string cache includes short IDs and empty fields as well as long text. This avoids repeated JSON
escaping or base64 conversion before the table builder compares equal typed values. Named scalar
methods and application codecs keep their own behavior. These buffers do not escape into decoded
state.

Reflection registrations keep immutable field and scalar-method descriptions. They contain no
application values. Derived type names and field declarations are cached only within one encoding
operation, after their normal checks. A later call can see newly registered application types.
Typed table arrays avoid a temporary slice of boxed values. Initial container and child-index
capacity is bounded before child validation. A large slice of invalid zero-size values must not
allocate storage for every child before the first error. Valid larger values grow during traversal.
Child indexes use bounded scratch storage; saved output never borrows it. Exact table sizing gives
storage one output allocation and
keeps event byte accounting equal to the uncompressed document. Scalar JSON leaves need no tree
index after JSON marshaling has validated them; objects and arrays retain full expansion checks.

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
Repeated long strings can share their decoded immutable value within that read. The decoder cache
ends with the read. Returned values never borrow mutable bytes from the encoded document.
This fast path applies only to built-in strings. Named scalars must run their JSON or text decode
methods, including validation. Their wire representation need not equal their logical value.
StreamChunk source IDs omit nil only; an empty non-nil slice is encoded as an empty array in both
component envelopes and storage tables.

RunView checks its compact replay projection against the archive by saved logical values. Go map
equality uses pointer-key addresses, so independently decoded copies can differ under DeepEqual
while containing exactly the same saved entries. The check uses ordinary equality first, then
compares complete typed trees when needed. It does not share decoded maps or omit the consistency
check. Real changes to history, runtime, checkpoint, or retained evidence still fail validation.
Complete archive validation uses the same rule when it reconstructs history from recorded events.
The fallback avoids expanded JSON document buffers and preserves codec failures as errors. It must
not apply a component-byte limit to an otherwise valid compressed archive during comparison.

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
  It also applies to component envelopes and uncompressed pending event documents.
- MaxExpandedBytes counts the complete logical JSON, including every reference use. Its zero default
  is 256 MiB. It also bounds decompressed table bytes before allocation.
  It separately bounds derived array types and reflective value allocations on both read and write.
- MaxExpandedValues counts logical JSON values, including repeated references and unknown fields.
  Its zero default is 4,194,304.
- Expanded document depth cannot exceed 512. Logical value depth cannot exceed 100 child levels.
  Fields, elements, map keys, and pointer targets add one level. Go interfaces and named type
  envelopes add none: they describe the same value. Write, read, and Clone share this rule.
  Framework codecs receive the current depth and count their own children. Clone must not restart
  codec-backed children at depth zero. Application codecs still own their private format limits.
- Each element or key type description permits at most 100 nested derived types. Read and write
  check this depth from the type root, independent of the containing value's depth. Registered
  types use their stable codec names. Unsupported type depth must fail before checkpoint acceptance.
- MaxBinaryBytes keeps the existing per-buffer binary bound.

Positive expansion limits replace the defaults. Encoded and binary limits remain unset at zero.
The expansion accounting uses checked addition before allocation; a small reference graph cannot
request exponential Go values or bypass a limit through an ignored field. Custom codecs still own
their internal allocation limits. These bounds measure the representation, not exact Go heap use.
Derived type checks divide before multiplication. This prevents nested array dimensions or a
container element count from overflowing reflection's address-space checks. A nil container still
needs its element type checked: a short type name can describe a very large inline array.
Direct Clone copies use the same whole-value and container-count bounds before allocation. A small
slice element can still request an oversized backing array. Maps check aggregate keys and values
separately; this does not count map overhead or combine them into one heap allowance. If a direct
copy cannot fit, Clone uses portable validation to retain the complete containing-field diagnostic.
It must not allocate the rejected copy before taking that path. MaxEncodedBytes does not select
whether these allocation checks apply. Binary buffers retain their separate MaxBinaryBytes rule.
There is no separate array-length cap. Such a cap can reject a type within the allocation bound,
especially an array of bytes or zero-size elements, after the writer has accepted it. The shared
size and depth rules keep saved custom components readable with the same registry and limits.
Validate a named envelope's underlying kind before decoding it at the same value depth. It must
contain the corresponding pointer, slice, array, or map envelope. A named byte slice uses bytes.
Nested named envelopes are invalid; accepting them would permit recursion without a depth bound.
Retain the underlying envelope's version, content, and assignment checks.
The document bound includes enclosing SaveBatch fields and is separate from logical value depth.
A component at its logical limit can exceed the document bound when placed in a batch. Keep the
save failure and earlier accepted state in that case; do not raise or skip the document bound.

Check a compressed document's integrity, base64, and declared decompressed size before allocating
the table buffer. Then validate every table and logical reference expansion before typed decoding.
The writer keeps plain tables if compressing them would exceed this decompressed-table bound.

Pending-event and Subscriber byte allowances still count the uncompressed document. Compression
must not permit a larger live queue. Small event accounting computes this exact length without
constructing the output buffer or hashing it. It runs the same codec and expansion validation.

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
