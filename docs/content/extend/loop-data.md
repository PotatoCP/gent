Use BasicLoopData when no custom fields are needed. A custom LoopData must implement CheckpointState
and TurnResetter. Every batch captures its complete accepted state, not only periodic captures.

## Separate run and turn fields

{{example:loop-data}}

ResetTurn performs local assignment only. It must not call a model, tool, or external service.
External work before admission could repeat after a failed save. Restore and Continue do not reset
turn fields.

## Capture and restore every field

{{example:loop-data-save}}

Encoding the value produces stable saved data. Do not return pointers to application fields that can
change after capture. Embedding BasicLoopData does not serialize added fields for you.

## Register the factory

{{example:loop-data-factory}}

Give that `config` to the Runner for your custom `loop`:

{{example:loop-data-runner}}

## Use the data inside Next

After a local file check succeeds, update the context's instance:

{{example:loop-data-use}}

The factory is not the data instance. Read `execCtx.Data()` inside the loop. Add a Recorder to the
same Config to keep progress after process exit. See [Save batches](/storage/save-batches/).

Add fields compatibly when their defaults preserve the old meaning. Preserve names, types, units,
and saved enum values while runs depend on them. Reject incompatible state explicitly. Gent does not
supply automatic migration or silently restart an unknown saved step.
