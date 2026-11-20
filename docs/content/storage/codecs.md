CodecRegistry owns saved format, integrity, and typed-value validation. Use its typed RunState load
method instead of duplicating validation in application code.

## Register a type

{{example:codec-register}}

Reflective encoding preserves Go field identities. A JSON tag does not preserve a renamed Go field.
Register types before reading saved data. Keep their saved meanings and versions stable while
compatible runs remain in use.

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
See [Restore and Continue](/storage/restore/) for the Ready, Invalid, and Incompatible branches.

## Store a complete document

Use `Marshal` and `Unmarshal` for SaveBatch and RunView. For a RunStateSnapshot, call
`MarshalRunState` or `UnmarshalRunState`. Each JSON document contains its format version, integrity
digest, type names, record layouts, and values. Repeated typed values can use references inside
that same document.
Loading does not need a dictionary from another file or process. No compression is applied.

Gent checks table references and logical expansion before it constructs typed values. Existing type,
version, declared-field, native-byte, and logical-state checks still apply. The decoder reads the
tables directly. It does not first build a full expanded JSON document.

`Encode` and `Decode` serve a different boundary: an explicit EncodedValue in custom checkpoint data
or another component. These envelopes remain self-contained. Do not use `json.Marshal` on one as a
replacement for a complete storage document. Diagnostic `json.Marshal(state)` is also not a saved
format.

This development format replaces the previous storage encoding. It does not add a migration reader
for saved files from that encoding. Batch receipts still identify the same complete typed values;
table allocation and shared-value selection do not change their identities.

## Set payload bounds

Create the bounded registry before registering saved types. This alternative sets `config.Codecs`;
register custom types on that registry and give the same value to the Recorder:

{{example:codec-bounds}}

`MaxEncodedBytes` bounds the complete stored document. `MaxBinaryBytes` bounds each decoded binary
buffer. Zero leaves those two limits unset. `MaxExpandedBytes` and `MaxExpandedValues` count the
logical JSON value after every shared reference is included. Zero selects 256 MiB and 4,194,304
values. Set positive values to replace these defaults for larger archives. Depth is also bounded.

Expansion checks include unused saved fields and run before custom decoders. Custom codecs must
still bound allocations inside their own data format. An integrity digest detects changed bytes;
it does not authenticate the writer.
