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

## Own the decoded state

Decode returns owned values. Successful Apply transfers retained batch values to the target.
Successful Restore transfers RunState to the context. Encode borrows stable state; do not mutate it
or transfer ownership at the same time. Configure CodecLimits for encoded and binary payload bounds.
See [Restore and Continue](/storage/restore/) for the Ready, Invalid, and Incompatible branches.

## Set payload bounds

On the shared Config before execution:

{{example:codec-bounds}}
