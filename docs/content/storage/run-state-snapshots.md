A RunStateSnapshot is a captured `RunState` or its encoded form. It is not a second public state
type. Batches remain sufficient for recovery. The RunStateSnapshot is optional replay acceleration.

## Capture at a save boundary

Extend the BatchRecorder from [Save batches](/storage/save-batches/):

{{example:snapshot-recorder-type}}

Insert this after encoding `batch` into `data` in a Recorder.Save implementation. `Every` is a
positive interval of event positions chosen by the application.

{{example:recorder-capture}}

The capture callback is valid only during its Save invocation. Do not retain the callback. The
returned RunState is independent and stable. It can be encoded after Save returns, but it must not
be published ahead of the accepted batch it describes.

## Use the recorder

The complete source defines `SnapshotRecorder`, which adds this capture step to `BatchRecorder`.
Use your `store`, the shared `codecs` registry, and a positive `interval` such as 100:

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
