`Recorder.Save` is the persistence boundary. Store each complete batch before returning nil. A
queue, socket write, or partial batch does not satisfy this contract.

## Implement the recorder

Gent calls `Save` with one fixed batch. Store every complete batch. The example uses
`batch.ShouldCaptureRunStateSnapshot(recorder.Every)` to decide when to call the capture callback.
Zero selects compaction, including a compaction event saved during a pause. A positive interval
also selects batches that cross a multiple of that many events.
[Choose when to capture](/storage/run-state-snapshots/#choose-when-to-capture).

The application supplies a `CodecRegistry` and a `ByteStore`. The store's Commit method takes the
batch metadata, encoded batch, and optional encoded RunStateSnapshot.

{{example:recorder-save}}

The complete source includes a memory store for tests. It does not survive process exit. A durable
store must atomically accept bytes, the expected cursor, and the exact duplicate receipt. A shared
store must also verify the current writer lease or fence in that transaction.

## Give the recorder to the Runner

Use your `store` implementation and a `codecs` registry from `gent.NewCodecRegistry()`. Register
custom saved types before use. Give the same registry to the recorder and the Runner:

{{example:recorder-runner}}

Create the execution context and call NewTurn as shown in [Hello World](/start/hello-world/).
The configured recorder receives saves from NewTurn and Continue, including the accepted task and
completed answer. Keep it on the Config used for [restoration](/storage/restore/) too.

## Accept a retry

`BatchId` identifies the exact batch contents. It is not an ordinal. An identical accepted retry
must return success without applying the batch again. Reject different content under the same
receipt. A failed acknowledgment can occur after commit; an error alone does not prove rejection.

## Savepoints and bounds

Gent saves admission, loop preparation and completion, managed tool boundaries, compaction, recovery
preparation, pauses, and completed outcomes. Model calls do not add independent saves. Unsaved model
work and its usage can repeat or disappear after interruption.

`RecordingConfig` sets pending event and byte bounds plus the save timeout. The defaults are 65,536
events, 64 MiB, and 30 seconds. Bounds are hard limits, not early-save triggers. The save context is
separate from model cancellation. Return storage errors; do not publish accepted output after a
failed or uncertain save.

See [RunStateSnapshots](/storage/run-state-snapshots/) for optional faster loading.

## Bound pending evidence

Set this on the same `config` before passing it to `executor.NewRunner`:

{{example:recording-bounds}}
