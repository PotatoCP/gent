`Recorder.Save` is the persistence boundary. Store each complete batch before returning nil. A
queue, socket write, or partial batch does not satisfy this contract.

## Implement the recorder

This adapter has a `CodecRegistry` and an application `ByteStore`. The store's Commit method takes
the batch metadata, encoded batch, and optional encoded RunStateSnapshot.

{{example:recorder-save}}

The complete source includes a memory store for tests. It does not survive process exit. A durable
store must atomically accept bytes, the expected cursor, and the exact duplicate receipt. A shared
store must also verify the current writer lease or fence in that transaction.

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

On the shared Config before execution:

{{example:recording-bounds}}
