# Executor test standards

Use these rules for executor, context, lifecycle, statistics, and limit tests. Agent-specific limit
coverage belongs in `agents/<agent_name>/agent_executor_limits_test.go`. General rules in
[AGENTS.md](../AGENTS.md) also apply. The locked contract is in
[req-multi-turn.md](../req-multi-turn.md); [history](../docs/content/storage/history.md) explains the public flow.

Tests must prove behavior visible to an application. Use public NewTurn, Continue, Restore, and
Recorder flows. Internal reducer tests can isolate format validation, but cannot replace the public
save/load tests. Do not modify private fields to simulate normal application behavior.

## Assertions and coordination

- Use table-driven tests for multiple scenarios, with named input, mocks, and expected types.
- Use testify/assert. Compare full known strings, values, statistics, results, and event sequences.
- Do not use substring, nonempty, or nonzero checks as the only proof of a known result.
- For time, compare against a measured lower bound. Do not use only `duration > 0`.
- Use channels for blocked callbacks and save boundaries. A sleep alone does not prove ordering.
- Assert the complete lifecycle order when it is part of the test. Focused API tests do not need
  unrelated lifecycle detail. Do not force nondeterministic parallel events into a false total
  order.
- Compare wrapped causes with errors.Is as well as checking the required diagnostic details.

## Public lifecycle

Cover a Runner configured with ordinary Config services. Nil or empty Limits must impose no hidden
execution limits. The same Runner must safely serve separate conversation contexts.

- NewTurn on a fresh context accepts the task and begins execution. Later NewTurn keeps RunId,
  history, summaries, custom run data, and cumulative counters, and allocates a new TurnId.
- NewTurn on idle unfinished state accepts closure of the old task and the new task atomically.
  Required recovery preparation must not execute queued old business actions to finish that task.
- Busy takes precedence and changes no task, data, subscriptions, or owned attempt resources.
- Continue executes an accepted unfinished task without resubmitting its input or resetting usage.
- Continue on a completed context returns NoActiveTurn. GetLastTurnResult reads the saved result.
- Finished/Success returns the accepted typed value. Refusal returns Finished/Refused and zero T.
- Restore performs local setup only. It does not call models, recover tools, reset turn fields,
  or run output processing again. Register codecs before loading or restoring.
- A clean completed or paused handle can execute again. A released or failed handle needs Restore.

Each iteration emits its before/after evidence in order. A terminal accepted outcome emits its
completion and accepted output only after Save succeeds. Stop and limit pauses emit pause evidence
at the approved pause save. There is no terminal error event/save for an unexpected code failure.

## Failure, Stop, and cleanup

Keep these contracts distinct in tests:

| Condition | Expected result and next action |
| --- | --- |
| Stop | Paused/Stopped after accepted pause save; Continue or NewTurn |
| Limit | Paused/LimitExceeded after accepted pause save; use a sufficient allowance to Continue |
| Worker context cancellation | Zero T, nil result, cause; restore accepted state |
| Unexpected operation failure | Zero T, nil result, wrapped cause; fix and Restore |
| Failed or uncertain Save | Zero T, nil result, storage failure; reconcile, then Restore |
| Subscriber failure | Stop/report that registration; execution and accepted state remain valid |
| Tool Call or Recover error | Safe guidance and private diagnostics; normal loop policy applies |

For unexpected errors, verify the previous completed result and accepted unfinished task remain
available. No provisional broken state is accepted. Diagnostics include stage and available
identities without changing errors.Is/errors.As behavior. Cleanup errors must not mask the cause.

Test Stop during model and tool work, repeated Stop, idle Stop, terminal-save races, and stale
application Stop commands. A later attempt must not inherit a previous request to stop. The owner
must wait for the active attempt to finish before submitting NewTurn. A terminal save can win a Stop
race; preserve that accepted result.

Release is final idempotent cleanup. It interrupts active work and waits for owned workers without
completing the task or making a terminal save. Verify stream producer cleanup and Subscriber worker
joining after success, pause, cancellation, errors, and panic. For panic tests, recover in the test
and check cleanup and the original panic; do not claim a panic becomes a normal execution result.

## Saved-state and archive equivalence

Prove both guarantees with real encoded SaveBatch values:

```text
Full batch replay == RunStateSnapshot + accepted tail == accepted logical execution state
Full archive replay == complete accepted RunView history
```

The second guarantee needs the earlier archive. A compact RunStateSnapshot cannot prove that old
archive records exist. Test compact state separately from full archive reconstruction.

At each approved savepoint, interrupt before Save, while Save is blocked, after storage commits
without acknowledgment, and after acceptance before dependent work. Use both ReAct and a custom Loop
without a required step enum. Restore custom nested fields, empty values, saved results, accepted
counters, native context, tool progress, and completed recovery preparation exactly.

Include no capture, stale capture, capture at the head, and capture plus a paged tail. An uncertain
retry must reuse the exact batch and must not rerun ResetTurn or duplicate task admission. Reject
wrong runs, gaps, reordering, conflicting duplicate batches, corrupt bytes, and missing codecs
without partial state mutation or external work. An exact duplicate is idempotent.

Use a real fixture ledger for tool effects. Cover Completed, NotStarted, Unresolved, recovery
errors, missing handlers, interrupted PTC, and supported parallel plans. Repeat interruption after
accepted After Recovery preparation; neither accepted effects nor completed recovery checks may
repeat.

Verify the original provider response and processed accepted answer both remain in the archive, with
correct source IDs. A rejected candidate never becomes accepted output. Provider continuation and a
later NewTurn need separate coverage, including both restore paths and repeated rejections.

## Statistics and limits

For each new statistic, prove its increment behavior and limit behavior. For a consecutive gauge,
also prove its reset. Use first-iteration and later-iteration cases.

- GetCounter is cumulative for the run. GetTurnCounter reads the current turn baseline difference.
- NewTurn accepts a fresh baseline. Continue and Restore keep the prior baseline and saved usage.
- All gauges are turn-scoped. NewTurn resets them and rebuilds context-size gauges from retained
  data.
- Scope defaults to LimitTurn. LimitRun changes counter measurement only; gauges remain turn-scoped.
- Separate conversations do not affect each other's counters, limits, or custom data.
- Include the same run across multiple turns and both replay paths to catch accidental usage resets.

Most exact and prefix rules pause when the value is greater than MaxValue. Exact iteration rules use
greater-than-or-equal at completed iteration boundaries. For positive N, permit N completed
iterations and prevent iteration N+1. Verify zero and already-exhausted allowances with the public
flow. Continue checks saved usage before starting another dependent action. A run allowance does not
reset when NewTurn accepts new input.

For every prefix rule, use at least two resource suffixes. A nonmatching suffix can exceed the
threshold without triggering that rule. Then exceed the matching suffix and compare the exact key,
rule, current value, all statistics, and Paused/LimitExceeded result. When several rules match the
same update, check the documented rule order and one LimitExceeded event for that decision.

Keep total-error counters and consecutive gauges distinct: a success can reset a gauge while the
cumulative count remains high. Recovered or invalid tool results follow their own gauge contract; do
not assume every response resets consecutive call errors.

## Subscriber, Hook, and concurrency coverage

- Compare both Accepted and Live delivery with the same recorded identities and concrete payloads.
- Live data stays provisional. Reconcile unsaved positions against the accepted archive on restore.
- Account for active callbacks in count and byte bounds. Cover one oversized event, queue overflow,
  callback failure, drain timeout, cancellation, and stop reporting with exact cursors.
- A disconnected display does not invalidate accepted execution. Replay can redeliver an event when
  callback success was uncertain; the UI deduplicates against its accepted view.
- Hooks run synchronously and can change only effective operation inputs. Compare the full frozen
  request/input seen by the operation and the saved archive. A failed Hook blocks the operation.
- Race capture against later saves and prove the captured value remains stable and independent.
- Share configured services across contexts and test normal cleanup after repeated use. No child
  hierarchy exists in this refactor; future subagent coverage must meet D116.

## Locations and verification

Use executor tests for lifecycle, agent tests for agent-produced limits, core tests for reducers,
and provider fixtures for exact wire contracts. `internal/tt` has reusable public-flow helpers and
expected event builders; read their current signatures before using them. Tests may build small
local adapters where that makes the scenario clearer.

For a new statistic, add increment, reset when applicable, first/later iteration limits, scope,
restore, prefix, and competing-rule cases. For a new agent, cover its complete produced statistics,
continued request messages, accepted output, and resource cleanup.

After code changes, run all required suites, without `-v` or output filtering:

```sh
go test ./... -count=1 -timeout 300s
CGO_ENABLED=0 go test ./... -count=1 -timeout 300s
go test -race ./... -count=1 -timeout 300s
```

Affected auxiliary modules need their own suites. Run configured live providers before release or
remote compatibility claims; a skipped test is not proof. Measure capture, encoding, loading,
allocations, and bytes separately from full archive replay for long histories and large active data.
