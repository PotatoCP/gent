# Multi-turn review: parallel cleanup and provider errors

Both findings are valid. The supplied probes reproduced both failures in normal and race runs
before these changes. No data race was reported. The fixes follow D103, D121, and D138 in
[the requirements](../../req-multi-turn.md).

## Parallel operation failure

A parallel worker returned its `ExecuteNext` error, but did not cancel the other workers. The
caller then waited for all workers. A cooperative tool waiting on its context could therefore
keep the failed attempt alive until the application canceled its owner context.

The failing worker now blocks further acceptance and cancels the attempt before the group joins
its workers. It uses the existing failure barrier, which also serves Hook and recording failures.
The internal helper does not add a public stop method. The error retains the operation, tool, call,
run, turn, and context identities. All workers finish before the failure returns to the application.
Earlier accepted savepoints remain available for recovery.

Expected cancellation keeps its separate contract. Stop still produces a recoverable pause.
An ordinary author `Tool.Call` error remains a handled tool result and does not cancel the group.

[Parallel regression tests](../../parallel_failure_test.go) cover operation failure, joined errors,
a cancellation value returned before the context ends, failure during Stop, normal Stop, and
handled tool failure. They check worker completion, exact saved phases, and execution results.
The failure cases also restore and continue through full replay and a RunStateSnapshot.

## Provider and response-validation errors

The managed stream obtained the provider error from `Response`, then replaced it when local
validation or evidence capture failed. An invalid partial response therefore hid the original
provider cause from `errors.Is` and `errors.As`.

The stream now joins the provider error with the local error before adding call and run details.
It returns both through the existing `Stream.Response` error path. Invalid output does not become
an accepted result, and this change adds no stream error field or savepoint.

[Stream regression tests](../../model_failure_causes_test.go) cover absent, valid, and invalid
partial responses; invalid progress content; invalid source links; and validation failure without
a provider failure. They compare the complete application error and accepted save phases, check
the provider cause, and continue the saved state through both replay paths.

## Verification

Checks completed on 18 November 2026:

- The full native suite passed: `go test ./... -count=1 -timeout 300s -p 1`.
- The full CGO-disabled suite passed with the same command and `CGO_ENABLED=0`.
- The full race command passed 30 packages. The `models` package reached the 300-second package
  timeout. A repeat of that complete package passed in 263.751 seconds with the same source,
  assertions, and timeout. Combined package checks confirm passing race results for all 31 root
  packages. The timed-out full command remains a failed run in the verification record.
- The current and previous external review probes passed normally and with the race detector.
- The saved reliability probes passed with `-race -tags gent_review` and the 300-second timeout.
- The separate `examples/chat` module passed native, CGO-disabled, and race suites. These checks
  include the other session's latest chat changes. Its Go sources stayed unchanged during and
  after these runs.
- Go formatting, changed-line whitespace, line lengths, report links, and package coverage passed.

The suites used `GOMAXPROCS=4`, `-count=1`, and a temporary build cache. Root source hashes stayed
unchanged across the final checks. Tests used fixtures and local HTTP servers; no paid provider
calls were made. No browser or client tests were repeated for these two runtime changes.

The model timeout occurred during batch encoding in a test that had just started. It reported no
assertion failure or data race. The repeat passed without a code change. The cause of the slower
aggregate run was not established during that repair. The
[timeout follow-up](model-package-timeout.md) records the profile evidence and shared decoder fix.

An earlier race run found an overbroad wait in the new parallel test. Its one-second bound included
later checkpoint encoding and the next model call, although both tools had finished. The same
one-second bound now checks the peer's exit. Full execution, saved-phase, and recovery checks remain
unchanged. The corrected test passed three race runs. An overlay of the original runtime functions
still makes the permanent tests fail on both reported defects, including the blocked parallel peer.
