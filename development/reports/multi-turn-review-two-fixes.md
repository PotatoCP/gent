# Multi-turn follow-up review fixes

All five follow-up findings were confirmed against the requirements and reproduced before the
fixes. This report supplements [the first six fixes](multi-turn-review-six-fixes.md). It does not
change the approved requirements.

## Causes and changes

1. **ReAct treated a failed plan as model input failure.** A `PrepareCalls` error became model
   steering when no plan was active. That error comes from the selected tool-chain code. ReAct now
   returns it to the application under D103. It adds the failing stage and preserves the cause for
   `errors.Is` and `errors.As`. No second model call or terminal save accepts the failed work.

2. **ReAct discarded an execution error when Stop canceled the context.** Its context check ran
   before its tool-chain error check. ReAct now preserves the operation error first, including a
   joined cancellation and application failure. The Runner can then apply the cancellation rule
   from the first repair. Pure cancellation still permits an accepted Stop pause.

3. **The internal script tool used the author-tool failure contract.** `run_code` called framework
   operations through the same return path as an ordinary `Tool.Call`. A failed inner Hook or
   tool-chain operation therefore became a handled tool error, and the outer plan could complete.
   The registry now shares validation and save barriers but separates invocation failures from
   handled author-tool errors. Internal failures return before result publication, program-state
   removal, or the outer tool-completed save. The script bridge preserves the original Go cause.

   Model-authored script errors still produce safe tool feedback. An unsupported
   `tool.parallelCall` is a script type error, because the model can correct that input. Errors
   returned by `PrepareCalls` or `ExecuteNext` follow D103. Ordinary author `Tool.Call` errors keep
   their documented handled-error path. No public interface or error marker type was added.

4. **Task validation allowed a tool role without a tool response.** Empty tool messages and plain
   tool text could pass admission, then fail at the provider. Tool-role content now requires a
   `ModelToolCallResponse` with a matching preceding request. Locally detectable invalid input is
   rejected before the initial save or model call. Valid assistant content and complete tool groups
   remain supported.

5. **The event sequence rename missed trace results and the chat projection.** The four position
   fields in `trace.StitchResult` now use `EventSequenceNumber`, including their JSON names. The
   chat projection now uses `EventSequenceNumber` and `eventSequenceNumber`. Client state, event
   inspection, browser selectors, examples, and tests use the same names. Display reconciliation
   still distinguishes Live attempts from Accepted events.

## Regression checks

- [Execution failures](../../execution_chain_failures_test.go) cover plan failure, execution
  failure, Stop with a joined failure, inner Hook failure, and script attempts to catch a framework
  failure. They also cover a later failure after an accepted inner tool result.
- The same tests compare full batch replay with the captured RunStateSnapshot and continue both
  through a corrected Runner. Accepted script effects are not repeated.
- [Task validation](../../internal/core/task_conversation_test.go) checks empty and plain tool
  messages, extra text in a tool-response group, and the existing valid conversation cases. Invalid
  admission makes no save or model call.
- [Trace results](../../trace/stitch_fields_test.go) check complete Go values and exact JSON for
  empty, contiguous, unsorted, and missing event positions.
- Existing script and tool tests preserve model correction, safe tool-error guidance, and the
  explicit parallel-tool requirement.
- The external review probes run through a Go overlay. They do not add temporary files to the
  repository.

## Verification

Checks completed on 18 November 2026:

- The full native suite passed: `go test ./... -count=1 -timeout 300s -p 1`.
- The full CGO-disabled suite passed with the same command and `CGO_ENABLED=0`.
- The full race suite passed: `go test -race ./... -count=1 -timeout 300s -p 1`.
  This command completed with exit status 0. All 31 root packages were included.
- The external review probes passed both normally and with the race detector.
- The saved reliability probes passed with `-race -tags gent_review` and the 300-second timeout.
- The separate `examples/chat` module passed native, CGO-disabled, and race suites.
- The chat client tests passed. The demo browser check passed for streaming, tools, retained input,
  both replay paths, event inspection, RunView, RunStateSnapshot, refresh, recovery, and mobile
  layout. The generated conversation screenshot was also inspected.
- Go formatting, changed-line whitespace, line lengths, report links, and the event-name search
  passed. Source hashes were unchanged across the final suite runs.

The Go suites used `GOMAXPROCS=4` and a temporary build cache. Package checks confirmed coverage
in all three root suites. No assertion or test timeout was weakened. These checks use
fixtures and local HTTP servers; no paid provider calls were run for this repair.
