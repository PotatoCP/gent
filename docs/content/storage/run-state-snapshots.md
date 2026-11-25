A RunStateSnapshot is a captured `RunState` or its encoded form. It is not a second public state
type. Batches remain sufficient for recovery. The RunStateSnapshot is optional replay acceleration.

## Choose when to capture

In `Recorder.Save`, call `batch.ShouldCaptureRunStateSnapshot(0)` to capture after compaction.
The method also detects a compaction event saved in a pause batch. This can occur when a custom
`NotifyCompacted` requests `Stop` and returns the attempt's cancellation error.

Use `batch.ShouldCaptureRunStateSnapshot(100)` to also capture when a batch crosses a multiple of
100 events. For example, a batch that advances from event 95 to event 103 recommends one capture
at 103. The same fixed batch and interval give the same result on a retry.

A capture lets recovery skip all batches through that position. Compaction can also reduce the
history retained in the RunStateSnapshot. Each complete batch is still required for the full
`RunView`. Complete batches alone are sufficient for recovery.

## Capture at a save boundary

Call `captureRunStateSnapshot()` during the `Save` call for the selected batch. The returned
`RunState` is independent and stable. Encode it with `MarshalRunState`. Publish the bytes in the
batch transaction or after that batch commits. The complete recorder shows these calls.
[Recorder.Save example](/storage/save-batches/#implement-the-recorder).

## Use the recorder

Use your `store`, the shared `codecs` registry, and an event `interval` such as 100.

{{example:snapshot-runner}}

Run tasks with this Runner as shown in [Hello World](/start/hello-world/). Gent supplies the capture
callback to Save. The application does not capture mutable execution state from another goroutine.

## Load the accepted tail

Decode the RunStateSnapshot, read `LastAppliedEventSequenceNumber`, and query stored batches after
that position. For example, a capture through event 95 needs only later accepted batches. See
[Restore and Continue](/storage/restore/) for both complete loading examples.

Do not use retained slice lengths as absolute history positions. References to archived records can
remain valid after those records leave execution state. A capture must not retain the whole archive
merely because active state refers to it.

Large custom LoopData can still make RunState large. Every SaveBatch must carry complete accepted
custom data and control state. A periodic RunStateSnapshot cannot repair missing batch data.
