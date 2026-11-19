# Multi-turn review: six runtime fixes

All six reported failures were reproduced with regression tests before the fixes. This report
records the causes, the changes, and the checks. It does not change the approved requirements.

## Causes and changes

1. **Stop hid joined execution errors.** A match from `errors.Is` proves that one error branch
   contains cancellation. It does not prove that all branches contain only cancellation. The
   shared cancellation check now examines every branch. A joined application failure reaches the
   caller. Gent does not save a pause checkpoint for that failed operation. Pure cancellation still
   permits the normal Stop pause.

2. **Attempt cleanup did not own stream delivery.** The attempt tracked model producers, but not
   the returned stream's drain worker. It now owns both. Cleanup closes and joins unread streams,
   including streams created from saved responses. Completed delivery releases its registration
   immediately so a long turn does not retain every completed stream. `Close` joins delivery;
   `Response` still waits for producer cleanup and final usage accounting.

3. **Hook validation occurred after the tool-ready save.** A valid model input could become invalid
   in `BeforeToolCall`, then enter saved state and become model correction guidance. Tool chains
   now supply `WithToolCallInputValidation`. Gent runs this check after the Hook and before it
   records or saves the effective input. A failure stops the operation and reaches the application.
   Invalid original model arguments still use the existing correction path. The tool is not called
   when the Hook produces invalid input.

4. **Task validation used the tool-result content rules for every message.** Admission now checks
   message roles, assistant content, and complete tool request/reply groups. It preserves native
   continuation data. It rejects missing, duplicate, or mismatched tool replies. Each complete tool
   group enters one history record so record selection cannot separate a reply from its request.
   User messages retain their separate saved origin for user-message retention.

5. **Turn reset treated the `gent:` prefix as a lifetime rule.** This removed the saved compaction
   baseline while cumulative counters remained unchanged. NewTurn now clears the known task
   continuation entries and preserves run state. A counter threshold therefore measures usage
   since the last compaction across later turns and restoration.

6. **Subscriber timeout hid joined callback failures.** Subscriber delivery now uses the same
   branch check as execution. Timeout remains the stop reason, and the complete unexpected
   callback error remains in `SubscriberStop.Error`. Pure timeout cancellation adds no unrelated
   failure. Subscriber failure still does not invalidate accepted execution.

## Regression checks

| Area | Checks |
| --- | --- |
| Stop | Pure and mixed cancellation; exact saved phases; accepted-state recovery |
| Streams | Unread live and replayed streams; repeated Release; completed stream cleanup |
| Hooks | Invalid schema and Go type; no tool-ready save or call; restore with a fixed Hook |
| Tasks | Assistant and native content; complete groups; invalid roles and groups; byte replay |
| Compaction | Reused and restored state; RunStateSnapshot plus tail; cumulative and prefix stats |
| Subscribers | Pure and mixed timeout errors; complete stop result and cursor |

The recovery checks compare full replay with the captured accepted state. A fixed runner continues
the saved task without admitting the task again. Existing Hook tests now expect an application
error for invalid effective input. Their original invalid-model-input correction cases remain.

Test sources:

- [Cancellation and subscriber failures](../../internal/core/cancellation_failure_test.go)
- [Managed stream cleanup](../../internal/core/managed_stream_cleanup_test.go)
- [Hook input validation](../../toolchain/hook_validation_test.go)
- [Task conversation input](../../internal/core/task_conversation_test.go)
- [Compaction across turns](../../compaction/turn_baseline_test.go)

## Verification

Checks completed on 18 November 2026:

- The full native suite passed with `go test ./... -count=1 -timeout 300s -p 1`.
- The full CGO-disabled suite passed with the same command and `CGO_ENABLED=0`.
- Race checks passed for all 31 packages returned by `go list ./...`. All tests used `-race`,
  `-count=1`, and the unchanged 300-second package timeout. No test filter excluded a case.
- The separate `examples/chat` module passed native, CGO-disabled, and race suites.
- Go formatting, changed-file whitespace, new-file line lengths, and report links passed.

The final full race command ended with SIGTERM after 20 packages passed. The system log had no
memory-kill record, and the sender could not be identified. The remaining packages completed in
three smaller groups: the recovery matrix, provider adapters, and remaining library packages.
The matrix took 151.666 seconds; provider tests took 194.120 seconds. A package-list check proved
complete coverage. Hash checks confirmed that the runtime fix files did not change between runs.

A diagnostic run with child-process signal tracing exceeded the large-stream test's ten-second
wait. The same test passed without that tracing in 5.312 seconds. No assertion or timeout changed
for verification. The diagnostic run is not part of the passing race results.
