# Checkpoint admission after a concurrent failure

The reported finding is valid. The supplied public-API probe failed in both normal and race runs
against the current code. A custom `ExecuteNext` failure and a `BeforeToolCall` Hook failure each
accepted an extra `PhaseToolReady` batch with value `1`. Full batch replay and a RunStateSnapshot
plus a nonempty accepted tail restored `1`, although the last accepted value before failure was `0`.
The Stop control passed. No data race was reported.

This is an ordering defect under D103. A mutex prevents data races, but the save path must also
order checkpoint admission against unexpected operation failure.

## Cause and fix

`flushHistory` checked `RecordingError` before custom-data capture. Capture and batch encoding can
take time. A parallel operation could fail during that work, set the failure record, and cancel
the attempt. The checkpoint then reached `Recorder.Save` without another failure check. Its
independent storage context correctly kept cancellation from interrupting storage, but did not
prevent admission of the failed attempt's provisional state.

The shared [save path](../../internal/core/run.go) now checks the failure record after capture,
batch encoding, and candidate validation. This check defines admission. It uses the same run mutex
as the failure record, so the two operations have one order. The flush mutex still permits only one
checkpoint save at a time. No additional public method, state type, or savepoint was added.

| Order | Result |
| --- | --- |
| Unexpected failure registered before admission | Reject the candidate; do not call the recorder |
| A fixed batch is admitted before failure | Finish that save under its normal storage contract |
| Stop or owner cancellation occurs | Keep the existing pause and observed-evidence contracts |

An admitted batch is fixed. A later failure cannot change its contents or withdraw a storage request
that can already have committed. If storage accepts it, retain that accepted state. If storage
returns an error, retain the exact uncertain batch for reconciliation. There is no failure check
after storage that discards a successful commit.

The run mutex is released before the recorder call. Holding it through capture or storage would
block failure reporting and can deadlock a callback that reads the run or waits for worker cleanup.
The comments on `Recorder.Save` and the internal admission check now explain this ordering.

A check immediately after custom-data capture would be incomplete. The new encoding test stops a
registered value codec during `BatchDigest`, then fails the parallel operation. The final admission
check must still reject that batch.

## Concurrent storage failure

The new tests also confirmed a related error-loss case. A recorder can fail after a parallel
operation has already failed. The original `stopRecording` kept only the first error. Managed tool
code then read that first error through `RecordingError`, hiding the storage failure and its
uncertain batch from the application.

The [failure record](../../internal/core/context_history.go) now retains distinct causes together.
Repeated propagation of a cause already represented by an error is recognized before joining.
Both the original operation failure and `PersistenceError` remain available through `errors.Is`
and `errors.As`. Error matching runs outside the run mutex because application-defined `Is` and
`Unwrap` methods can inspect the context.

An incoming joined error can contain both the earlier failure and a new storage failure. Keep that
complete error. Do not discard it because it also matches the earlier cause. A private revision
check retries the update if another failure arrives during matching. This avoids replacing a newer
failure record while keeping application error methods outside the lock.

## Permanent regression coverage

[The regression test](../../checkpoint_admission_test.go) uses public execution interfaces and
deterministic barriers. Its 15 cases cover:

- Custom `ExecuteNext` and built-in Registry Hook failures during capture, encoding, and storage.
- Storage rejection and a committed save whose acknowledgment fails after an operation failure.
- Storage errors that join the earlier operation failure with a new cause.
- Stop during capture, encoding, and storage.
- Owner cancellation during capture and storage.

Each case checks the complete saved-phase sequence and custom-data values. Failed calls return
zero output and no `ExecutionResult`; successful Stop returns the complete paused result. Storage
failures retain the submitted batch. A custom error method reads the context to exercise error
inspection outside the run lock. No canceled tool starts its external effect.

Both replay paths use fresh codec and state objects. The RunStateSnapshot path has a nonempty tail.
Each restored run then calls Continue, completes normally, and preserves the expected custom value.
The storage tests delay RunStateSnapshot capture until after failure to check that the callback
uses the fixed candidate rather than recapturing changed live data.

The permanent tests were also run against the original runtime through a Go overlay. They reproduce
both the admission defect and loss of the concurrent storage error. The overlay changes no source
files in the shared checkout. Exactly eight defect cases failed; all seven control cases passed.

## Verification

Final verification completed on 19 November 2026:

| Check | Result |
| --- | --- |
| Permanent regressions, three race runs | Pass |
| Supplied review probe, normal and race | Pass |
| Both earlier review probe sets, normal and race | Pass |
| Reliability probes with `-race -tags gent_review` | Pass |
| Full native suite | Pass; all 31 root packages accounted for |
| Full CGO-disabled suite | Pass; all 31 root packages accounted for |
| Full race suite | Pass; all 31 root packages accounted for |
| Chat module, native, CGO-disabled, and race | Pass |

The full suites used `GOMAXPROCS=4`, a temporary build cache, and these commands:

```sh
go test ./... -count=1 -timeout 300s -p 1
CGO_ENABLED=0 go test ./... -count=1 -timeout 300s -p 1
go test -race ./... -count=1 -timeout 300s -p 1
```

The separate `examples/chat` module used the same commands. The full root race command exited with
code 0. No timeout or assertion was relaxed. No data race was reported. Tests used fixtures and
local HTTP servers; no live-provider credentials were loaded.

Root source hashes stayed unchanged across the final runs and the final source check. Another
session added a temporary `examples/chat/codec_explorer_probe_test.go` before the chat checks, then
changed and removed it afterward. It is outside this fix. The chat application and its permanent
tests stayed unchanged and passed all three suites. This session did not change that probe.
Go formatting, changed-line whitespace, line lengths, report links, and package coverage passed.
Earlier partial verification was stopped
to include the confirmed joined-storage-error case; it is excluded from these final results.
Logs, original-source overlays, and verification records remain in `/tmp/gent-checkpoint-admission`
for this workspace session.
