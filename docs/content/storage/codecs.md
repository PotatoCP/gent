CodecRegistry owns saved format, integrity, and typed-value validation. Use its typed RunState load
method instead of duplicating validation in application code.

## Register a type

{{example:codec-register}}

Reflective encoding preserves Go field identities. A JSON tag does not preserve a renamed Go field.
Register types before reading saved data. Keep their saved meanings and versions stable while
compatible runs remain in use.

Names must contain valid UTF-8. Valid Unicode names are supported, including the replacement
character U+FFFD. Gent rejects malformed bytes before changing the registry. JSON would replace
those bytes and change the saved name. The changed name could be unknown or belong to another type.

Use an application name such as `app.progress`. Both `RegisterType` and `Register` reject these
reserved names: `bytes`, `nil`, `pointer`, `slice`, `array`, `map`, `time`, `error`,
`encoded-value`, `raw-json-bytes`, `raw-json-nil`, `any`, `content`, and `error-interface`.
Names must not start with `*`, `[`, or `map:`. These prefixes describe pointers, slices, arrays,
and maps in saved data.
Names already bound to a type are also unavailable. A rejected registration leaves the registry
unchanged. Correct the name and register the same type again before use.

This rule keeps application names separate from saved type syntax. Without it, a registration could
change a saved value's type or prevent restoration of an unrelated built-in container.

## Supply a custom codec

This codec stores the Progress value from [LoopData](/extend/loop-data/):

{{example:codec-custom}}

Register it before use:

{{example:codec-custom-register}}

The codec must validate its representation and reject unsupported values. Compatible new fields need
valid defaults. Gent does not supply automatic custom-data migration.

## Give the registry to the Runner

Use the `codecs` registry after registering your saved types and custom codecs. Here `loop` is your
configured agent loop:

{{example:codec-runner}}

When adding a Recorder to this Config, give its implementation the same registry. The
[Save batches example](/storage/save-batches/#give-the-recorder-to-the-runner) shows both fields.
Use this registry for [decoding and restoration](/storage/restore/) too.

## Own the decoded state

Decode returns owned values. Successful Apply transfers retained batch values to the target.
Successful Restore transfers RunState to the context. Encode borrows stable state; do not mutate it
or transfer ownership at the same time. Equal saved values can share bytes inside a document.
Each loaded occurrence is an independent mutable Go value. Shared bytes do not create shared
pointers, maps, or slices in the recovered state.
Named scalar values keep their JSON and text decoding methods, including validation errors.
Only built-in strings use the shared literal decoding path.
Clone follows those methods too, including errors, even when no encoded-byte limit is set. A direct
copy is used only when it has the same type and value as a portable round trip. This keeps frozen
Subscriber payloads equal to their accepted archive values. JSON methods keep precedence over text
methods. Ordinary immutable scalars still use cheap direct copies.
See [Restore and Continue](/storage/restore/) for the Ready, Invalid, and Incompatible branches.

Distinct pointer keys can contain equal values. Gent keeps every map entry and uses saved keys and
values to define a stable order. It does not use pointer addresses. The same data must produce the
same batch receipt before and after loading, including keys inside structs, arrays, or interfaces.
Zero-size pointer targets also have independent storage. Gent adds backing storage in memory so
Go cannot combine distinct empty targets at one address. No address is stored in the document.

## Keep saved values readable

Ordinary strings must contain valid UTF-8. Gent rejects malformed bytes before encoding a component
or a storage document. This includes nested fields, long strings, and map keys. JSON would replace
those bytes with U+FFFD, which could change content or combine distinct map keys. Valid Unicode,
including an actual U+FFFD character, remains supported. Use `[]byte` for arbitrary binary data.
Explicit application codecs and scalar JSON or text methods keep their own representation contract.
Framework error text and codes, stream source IDs, and component type names follow the same rule.

Built-in `time.Time` values use JSON timestamps. This representation retains the date, time, and
offset. It does not retain location identity or a monotonic clock reading. Gent rejects maps whose
keys would become equal after that normalization. The check also covers times inside struct, array,
and interface keys. Other distinct key fields or independently owned pointers can keep entries
distinct. Ordinary timestamps and maps with distinct saved instants remain supported.

Errors use `*gent.SavedError` by default. This keeps their text, public codes, and saved causes.
`RegisterType` does not change this rule. An `error` or `any` field can receive the saved value.
A concrete error field or container, such as `[]MyError`, might not accept `*gent.SavedError`.
Gent rejects that non-nil value before writing. Use `Register` with an explicit ValueCodec for each
concrete error type that must be restored. A value type and its pointer type are separate bindings.
Custom codecs keep precedence and must return the type they register.
Nil error pointers and empty containers remain supported when their declared types are registered.
They have no non-nil error value that needs conversion.

Within an error or any interface, a typed nil error becomes nil. Gent checks this conversion and
time normalization together for map keys, including keys inside arrays and structs. A map with both
a nil key and a typed nil error key would lose an entry. Gent rejects it before writing. A unique
nil key, nil/empty maps, independently owned non-nil errors and pointers, and explicit ValueCodec
representations remain supported. Nil error interfaces use the same saved nil representation before
and after loading, so a supported map keeps its batch receipt.

These write checks also apply to Clone. A failed `SetCheckpointState` leaves its earlier value
unchanged. If this failure ends execution, fix the input or code, restore the accepted state, and
call Continue. Do not resubmit the accepted task.
See [Execution control and failures](/run/control/).

## Store a complete document

Use `Marshal` and `Unmarshal` for SaveBatch and RunView. For a RunStateSnapshot, call
`MarshalRunState` or `UnmarshalRunState`. Each JSON document contains its format version, integrity
digest, type names, record layouts, and values. Repeated typed values can use references inside
that same document.
Loading does not need a dictionary from another file or process. Large documents use lossless Snappy
compression when the final JSON is at least one eighth smaller. Documents below 8 KiB and documents
without that saving keep plain tables. The v3 reader accepts both representations.

For directly readable tables, create the registry with
`gent.WithCodecCompression(gent.CodecCompressionNone)`. Compression is a storage choice. It does not
change batch receipts, retained content, or native bytes. The returned bytes can be stored or sent
without re-encoding. Compression does not change the separate JSON shape of your UI events.
Pending events and subscriber queues still count uncompressed encoded bytes. Compression must not
increase their live memory allowance. These counts remain bounds on representation, not total heap.

Gent checks table references and logical expansion before it constructs typed values. Existing type,
version, declared-field, native-byte, and logical-state checks still apply. The decoder reads the
tables directly. It does not first build a full expanded JSON document.

`Encode` and `Decode` serve a different boundary: an explicit EncodedValue in custom checkpoint data
or another component. These envelopes remain self-contained. Do not use `json.Marshal` on one as a
replacement for a complete storage document. Diagnostic `json.Marshal(state)` is also not a saved
format.

## Upgrade Gent with saved runs

**Starting with storage v3, upgrading Gent requires no data migration.** Keep the bytes you stored.
Every later Gent release must read those batches, RunStateSnapshots, and archives. Restore the
accepted state with your usual calls. Use Continue for an unfinished task or NewTurn after a
completed turn. You can append new batches to the same run. Existing receipts stay valid.

Every document has a top-level integer `formatVersion`. V3 stores plain tables as an object or a
Snappy block as a base64 string in `data`. The run schema and component versions are independent.
Gent reads its old types and representations. Your application supplies compatible readers for its
own custom data and the recovery binding for any unfinished tool operation. A provider can still
decline a new request that uses expired provider context.

V1 and v2 were development formats. The compatibility guarantee starts with v3. An older Gent
reader can report a newer format as incompatible. Diagnostic JSON remains separate from storage.

## Set payload bounds

Create the bounded registry before registering saved types. This alternative sets `config.Codecs`;
register custom types on that registry and give the same value to the Recorder:

{{example:codec-bounds}}

`MaxEncodedBytes` bounds stored documents, component envelopes, and uncompressed pending events.
`MaxBinaryBytes` bounds each decoded binary buffer. Zero leaves these two limits unset.
`MaxExpandedBytes` and `MaxExpandedValues` count the logical JSON value after every shared reference
is included. Zero selects 256 MiB and 4,194,304 values. Use positive values to change these defaults
for larger archives. Depth is also bounded.
`MaxExpandedBytes` also bounds decompressed tables before allocation. Logical reference expansion
is checked after decompression. Immutable strings can share decoded storage; mutable values cannot.
The same byte limit checks each derived array type and reflective value allocation before it is
created. A short saved type name cannot request an array larger than this limit. Writers apply the
same checks. Clone checks each direct copy before allocation, even with no encoded-byte limit.
These checks do not bound total Go heap use or container overhead.

Each container element or key type description can have at most 100 nested derived types. Read and
write use the same limit, separate from value nesting. A nil container still needs its type checked.
Array length has no separate cap; the byte-size bound applies, including to zero-size elements.
An unsupported type fails during encoding, before `SetCheckpointState` changes the saved component.

Logical values can have at most 100 child levels. Fields, elements, map keys, and pointer targets
add a level. Named type envelopes and Go interfaces describe the same value and add no level.
Encoding, decoding, and Clone use this rule. A named envelope must contain its matching underlying
container kind; extra named envelopes are invalid. Codec-backed children keep their current depth.
The separate 512-level document bound includes the surrounding SaveBatch fields. A component that
fits by itself can exceed that bound inside a batch. A failed save does not accept the new state.

Expansion checks include unused saved fields and run before custom decoders. Custom codecs must
still bound allocations inside their own data format. An integrity digest detects changed bytes;
it does not authenticate the writer.
