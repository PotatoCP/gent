A RunStateSnapshot is a captured `RunState` or its encoded form. It is not a second public state
type. Batches remain sufficient for recovery. The RunStateSnapshot is optional replay acceleration.

## Capture at a save boundary

Insert this after encoding `batch` into `data` in a Recorder.Save implementation. `Every` is a
positive interval of event positions chosen by the application.

{{example:recorder-capture}}

The capture callback is valid only during its Save invocation. Do not retain the callback. The
returned RunState is independent and stable. It can be encoded after Save returns, but it must not
be published ahead of the accepted batch it describes.

## Load the accepted tail

Decode the RunStateSnapshot, read `LastAppliedEventSequenceNumber`, and query stored batches after
that position. For example, a capture through event 95 needs only later accepted batches. See
[Restore and Continue](/storage/restore/) for both complete loading examples.

Do not use retained slice lengths as absolute history positions. References to archived records can
remain valid after those records leave execution state. A capture must not retain the whole archive
merely because active state refers to it.

Large custom LoopData can still make RunState large. Every SaveBatch must carry complete accepted
custom data and control state. A periodic RunStateSnapshot cannot repair missing batch data.
