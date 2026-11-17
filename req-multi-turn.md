# Multi-turn execution and RunStateSnapshots

Status: **Requirements ready for implementation.** This document defines the current approved
contract through D143. This cleanup changes documentation only; it does not implement the feature.

The latest locked decision governs each topic. Superseded declarations and review questions have
been removed. Decision IDs below identify approval sources; they do not define competing versions.
The cleanup report records the removed alternatives. The research appendix describes dated source
observations, not the target Gent API or proof that this refactor already works.

## Scope and design rules

Use one agent run for the complete conversation. Keep its RunId across tasks, accepted history, tool
and model evidence, compacted summaries, and custom LoopData. A new user task starts a new TurnId
within that run. The design does not require one process or an unbounded model request. Long
histories make a compact RunStateSnapshot plus a bounded accepted tail essential. (D05/D30)

Support every Gent provider through the normal managed execution path. Every turn uses ordinary
tools, output validation, Hooks, Subscribers, cancellation, limits, and Recorder.Save. Publish
accepted output only after its save succeeds. (D18/D27/D29/D35/D100)

Keep public interfaces small and explicit. Use one path for each operation, with good defaults and
customization through the same interface. Prefer pointers for struct parameters and results. Use
named string enum types and documented typed constants. Use ctx for context.Context and execCtx for
ExecutionContext; use role-specific names when several contexts share a scope. Expected caller
conditions use result structs and typed statuses. Unexpected operation failures use error. Copy only
for an independent mutable value or a stable captured value. (D25/D36/D49/D70)

Loop-specific models, prompts, retries, and checks stay on Loop. Common services and execution
policies belong in gent.Config. In particular, shared SteeringMessages supplies tool, recovery, and
media guidance; it does not absorb ReAct repetition or response-length settings. Shared Runner
configuration is fixed while in use. Create another Runner and restore accepted state to change
configuration. Shared service objects retain their own ownership and concurrency rules.
(D33/D34/D38)

Support evolved code, tools, limits, steering, and custom data wherever saved state remains usable.
Do not require a matching Runner version or a registry of old Runners. Preserve accepted facts and
counters; current configuration governs future work. Report unusable saved data explicitly, and use
D103 for invalid current code/configuration or unusable native requests. Do not silently erase
accepted evidence. Custom-data evolution follows D76 below.

This refactor removes child execution and its exclusive APIs/state. It defers replacement subagents,
live user steering/queues, inline system-instruction replacement, and provider-native compaction
APIs. Generic LLM summarization remains in scope. The example chat application follows the refactor.
Do not add a session wrapper, separate admission API, custom executor, saved-state migration API,
native-context reset/conversion API, or custom-data update registry. These are scope limits, not
unresolved prerequisites. (D23/D28/D32/D81/D85/D116/D119)

## Recovery, archive, and ownership guarantees

When an application correctly stores complete SaveBatch values through Recorder.Save and follows the
LoopData and managed-execution contracts, Gent must restore the same logical agent state at every
accepted savepoint. The application can then Continue the task or use NewTurn on idle state.

```text
full batch replay == RunStateSnapshot + accepted tail == accepted logical state
```

Both replay paths end at the same accepted event sequence position. Include custom data, framework
continuation, active/required history, identity allocation, counters, accepted output, managed
operation identities, and recovery evidence. RunStateSnapshots are optional acceleration; batches
alone must restore all of this state. This is an implementation and test invariant.
(D40/D52/D53/D62/D63/D65/D77)

The guarantee covers logical state and continuation. It does not serialize a Go stack, promise
identical future model output, or undo external effects. Reuse accepted managed results and
completed recovery preparation. Use managed recovery for unfinished external operations. A custom
Loop.Next restarts at its entry point on restored data; its author owns repeated local work and new
operation identities. (D61/D72)

When the application retains the complete accepted batch archive, RunView must reconstruct the full
accepted history without rerunning models, tools, or output processing. Preserve original prompts,
instruction versions, effective requests, responses, readable assistant content, provider-native
evidence, tool results, validation decisions, accepted output, and compaction changes. Compaction
changes the active selection; it never deletes archived originals. An opaque native record alone is
not a sufficient readable archive. (D30/D98/D104/D110)

The archive guarantee ends at the last accepted savepoint. Unsaved live output can be lost, and
provider reasoning that was never exposed cannot be reconstructed. A compact RunStateSnapshot and
its tail restore execution but cannot replace the earlier archive for full troubleshooting.
Historical replay and future Subscriber delivery are separate operations.

Put both guarantees in the Gent design guidance. Keep the recovery invariant in Go documentation on
Recorder and the RunState capture/replay APIs, including their continuation and custom-loop limits.
Do not leave these constraints only in this requirement document. (D65/D77/D98)

### RunState and RunView

Use one public RunState for recoverable execution data, capture, encoding, and decoding. A
RunStateSnapshot is a stable captured RunState or its encoded form, not another public wrapper.
RunView is the complete accepted archive. Normal execution restoration must not construct a full
RunView. (D30/D64/D78/D118)

RunState has private typed groups for identity/cursor, turn and outcome, framework continuation,
active and required context, counters and allocation, plans/recovery evidence, and versioned custom
data. It retains the last completed result. Custom saved steps remain optional. Exact private
fields, package placement, indexes, byte format, and allocation mechanics are implementation choices
under these guarantees, not additional public interfaces. Large custom data, active media, or the
last result can still make retained state large.

Keep absolute history positions and identity-allocation high-water marks separate from retained
slice lengths. Removing old entries from execution state must not reduce its history revision,
reuse an accepted identity or change the order of later entries. For example, `len(History.Entries)`
cannot supply the next absolute position after earlier entries leave RunState.

An accepted archive reference, such as a source ID or compaction input ID, stays valid when its
source record leaves RunState. Validate new references at acceptance and preserve the evidence
needed to replay them. Absence from retained state alone must not invalidate an accepted reference;
an unknown ID alone does not prove that a source record exists. Retain content needed for active
requests and managed recovery. Keep other source records in the archive. Do not pull every record
reachable through archive references into each RunStateSnapshot: that would make capture and
retained memory grow with the complete history and defeat compact restoration. (D30/D78/D98/D118)

| Operation | Ownership contract |
| --- | --- |
| Decode | Caller owns the returned RunState |
| Applied batch | Target takes ownership of retained batch values |
| Duplicate or rejected Apply | Neither input changes; no ownership transfer |
| Ready Restore | ExecutionContext takes ownership of RunState |
| Failed Restore | Caller retains RunState |
| Encode | Borrows a stable state; no concurrent mutation or transfer |
| Recorder capture | Returns an independent stable RunStateSnapshot |

Do not add a full defensive copy during Restore or copy the full archive for each Apply. A caller
that needs two independently mutable replay targets supplies independent decoded inputs. Borrowed
getters and event payloads remain read-only. (D78/D118)

### Identity and naming

Accepted event positions form one consecutive sequence per RunId. The first event is 1 and an empty
accepted cursor is 0. BatchId is a digest, not a batch ordinal. Live positions are provisional and
can be reused after an interrupted attempt. AttemptId distinguishes those live attempts. TurnId
identifies the task across Continue, restoration, and repeated attempts; NewTurn accepts a new
TurnId within the same run. (D66/D105/D130/D133)

Use EventSequenceNumber, PreviousEventSequenceNumber, and LastAppliedEventSequenceNumber for these
ordered positions. Apply the rename to RecordedEvent, BaseEvent, Checkpoint, SaveBatch, RunView,
RunState getters, wire fields, trace cursors, tests, examples, comments, guides, Quick Start, and
code generators/templates together. Use RunStateSnapshot and savepoint consistently. No development
API compatibility aliases are required. Exact identity-generation and encoding formats are private
implementation choices. (D59/D64/D66)

## Execution APIs and task input

The public execution family is on executor.Runner[T]. Restore prepares resources and state; Continue
executes the same saved task. NewTurn accepts and executes the first or a later task. Gent keeps
shared restoration internal; applications do not supply RestoreBindings. There is no second public
Start, Execute, Resume, admission-only, or low-level restore path. (D31/D35/D39/D67)

```go
// executor package; Locked D35, D37, D39, and D67 signatures.
func NewRunner[T any](loop gent.Loop[T], config *gent.Config) *Runner[T]

func (r *Runner[T]) NewExecutionContext(
    ctx context.Context, name string,
) (*gent.ExecutionContext, error)

func (r *Runner[T]) Restore(
    ctx context.Context, state *gent.RunState,
) (*gent.RestoreResult, error)

func (r *Runner[T]) Continue(execCtx *gent.ExecutionContext) (T, *gent.ExecutionResult, error)

func (r *Runner[T]) NewTurn(
    execCtx *gent.ExecutionContext, task *gent.Task,
) (T, *gent.ExecutionResult, error)

// GetLastTurnResult reads the last accepted completed-turn result without executing the Loop.
// A nil ExecutionResult means no completed-turn result exists.
func (r *Runner[T]) GetLastTurnResult(
    execCtx *gent.ExecutionContext,
) (T, *gent.ExecutionResult, error)
```

NewExecutionContext allocates task-independent LoopData through the configured factory and prepares
resources. The first NewTurn supplies the task and performs the turn reset before its accepted
initial save. Restore allocates the configured instance and restores saved fields even when the
previous turn completed. It does not execute the Loop, call/recover tools, reset fields, or process
accepted output. (D39/D48/D83/D120)

ExecutionContext owns the accepted Task and exposes it through Task(). LoopData has no GetTask.
ExecutionContext.Config() *Config exposes the read-only shared configuration. Keep Loop.Next's
existing signature and custom-loop extension point. The Runner owns lifecycle, managed recovery,
limits, persistence, and attempt cleanup. (D37/D47/D72)

### Task and effective system instructions

The approved Task declaration is:

```go
type Task struct {
    Instructions []*ModelMessage
    Messages     []*ModelMessage
}
```

Nil Instructions inherits the accepted system prefix. A non-nil slice replaces the complete prefix;
a non-nil empty slice clears it. Preserve this distinction through capture, codecs, and replay.
NewTask(parts...) creates one user message and leaves Instructions nil. Messages contains new
ordered conversation input, with at least one user message. Validate Instructions as system prefix
messages and reject system instructions in Messages. Caller-authored assistant/tool context is
permitted when it forms a valid request. Reject locally detectable invalid task/setup before
acceptance. (D85/D89/D119)

Accept instruction replacement and input in the same NewTurn save before dependent work. Continue
and Restore use the accepted saved prefix, even if application code changed. Compaction preserves
that prefix unchanged. Keep previous instruction versions in the archive, not as an active second
prefix. A normal request uses the effective prefix and selected conversation; it does not prepend
the complete Task again or restore messages already omitted from active context. (D85/D89)

Gent owns instruction lifetime and acceptance; adapters own native representation and usable
continuation. Never replace prefix replacement with appended instructions or discard native data
silently. Validate effective native requests after Hooks through normal request construction;
invalid or unusable input follows D103. No new request-check result/interface or provider-state
reset/conversion API is required. (D87/D103/D119)

Instruction replacement can invalidate a cached request prefix, increasing input work and latency.
Document that cost on instruction input and NewTurn without promising a fixed charge or cache hit.
Old conversation references can remain; summaries must distinguish past decisions from current
instructions. Gent guarantees delivery of accepted instructions, not model obedience. Deterministic
requirements belong in tools, output validation, or execution policy. System instructions still
consume context budget; an unusable request, including an oversized prefix, follows D103.
(D86/D88/D103)

### Execution results

Use one ExecutionStatus and the existing TerminationReason. Do not add TurnStatus. These are the
complete current status and result declarations. (D68/D69/D105/D111/D123/D124/D133/D143)

```go
type ExecutionStatus string

const (
    // ExecutionFinished means a completed-turn result was accepted.
    ExecutionFinished ExecutionStatus = "finished"

    // ExecutionPaused means execution stopped with the saved turn still unfinished.
    ExecutionPaused ExecutionStatus = "paused"

    // ExecutionBusy means another call owns execution of this context.
    ExecutionBusy ExecutionStatus = "busy"

    // ExecutionNoActiveTurn means Continue has no unfinished turn to execute.
    ExecutionNoActiveTurn ExecutionStatus = "no_active_turn"
)

type ExecutionResult struct {
    Status            ExecutionStatus
    TurnId            string
    TerminationReason TerminationReason
    Output            []ContentPart
    Refusal           *ModelRefusal
    ExceededLimit     *LimitExceeded
    CancellationCause *SavedError
}

type TerminationReason string

const (
    // TerminationSuccess means the turn completed with accepted output.
    TerminationSuccess TerminationReason = "success"

    // TerminationRefused means the turn completed with a provider refusal.
    TerminationRefused TerminationReason = "refused"

    // TerminationCanceled means the unfinished turn was closed, such as by NewTurn.
    TerminationCanceled TerminationReason = "canceled"

    // TerminationStopped means Stop paused the turn without completing it.
    TerminationStopped TerminationReason = "stopped"

    // TerminationLimitExceeded means a limit paused the unfinished turn.
    TerminationLimitExceeded TerminationReason = "limit_exceeded"
)

type LimitExceeded struct {
    Limit        *Limit
    MatchedKey   StatKey
    CurrentValue float64
}
```

Status determines the next action. Finished means an accepted completed-turn result, not merely that
an execution method returned. Paused means the saved task remains unfinished; no Go stack or worker
remains active merely because it is paused. Busy prevents overlapping execution. Continue returns
NoActiveTurn when no unfinished task exists. Busy takes precedence over other admission checks and
cannot release the active call's resources. Expected cannot-start results return zero T and nil
error without changing accepted state or returning an old answer as new output.

Output contains saved accepted delivery content for success; typed T is the processed application
value. Refusal supplies refusal details. ExceededLimit supplies stable effective rule, MatchedKey,
and CurrentValue under the selected turn/run scope. CancellationCause supplies diagnostics for a
completed canceled task. Cannot-start results have no termination/output details. Results and
reachable values are read-only. (D69/D123/D124)

Gent converts CancellationCause with the existing SaveError and uses normal saved-result encoding.
Preserve message text, supported codes, and nested causes. Do not promise restoration of arbitrary
private Go error types, publish diagnostics as chat content, or add an application serializer.
CurrentValue uses the existing float64 measurement; use the integer counter APIs for exact
accounting. Save the limit details with the pause so later configuration cannot change that stop's
explanation. (D124)

Control flow uses Status and TerminationReason. Unexpected execution errors use the returned error,
not ExecutionResult.Error or a completed error result. (D103/D123/D134)

| Return case | T | ExecutionResult | error | Caller action |
| --- | --- | --- | --- | --- |
| Accepted successful completion | Saved value | Finished / Success | nil | Read or NewTurn |
| Accepted refusal | Zero | Finished / Refused | nil | Read or NewTurn |
| Accepted user Stop | Zero | Paused / Stopped | nil | Continue or NewTurn |
| Accepted execution-limit pause | Zero | Paused / LimitExceeded | nil | Continue or NewTurn |
| Busy / NoActiveTurn | Zero | Approved call status | nil | Handle that status |
| Owner/process interruption | Zero | nil | Cause | Restore accepted state |
| D103 failure | Zero | nil | Wrapped cause | Fix, Restore, Continue |
| Failed or uncertain Save | Zero | nil | Storage failure | Reconcile, Restore |

A limit must permit the dependent work before Continue can proceed. Stop never resets usage. A new
task resets only its approved turn state; run limits remain. A terminal save already in progress can
win a Stop race; preserve the accepted result. NewTurn returns Busy until the old attempt ends.

On idle unfinished state, NewTurn accepts old-task closure and new input/reset atomically after
required managed recovery. The old task's Finished/Canceled result is retained under D74/D133; the
call returns the new task's result. A pause alone preserves the previous completed result.
GetLastTurnResult never treats a paused turn as completed or reruns output processing.

Stop and limit pauses preserve unfinished external-operation evidence. Cleanup must not invent a
completed effect, rerun an accepted effect, or execute queued business calls to force completion.
Use the managed recovery contract and its existing save boundaries. Keep unexpected code failures on
D103's error path and reconcile storage failures before another writer continues.

**Locked flexible NewTurn admission D133, 2026-11-17.** The owner approved NewTurn on an idle
unfinished turn and requested removal or revision of the superseded restrictions. Users can stop
execution and submit a new task without first completing the previous task. Keep the existing
NewTurn signature and synchronous typed result. Reuse managed recovery and the normal NewTurn save
boundary; do not add a queue, session wrapper, or separate public close/admission operation.

NewTurn accepts a task on a fresh context, after a completed turn, or on an idle unfinished turn. It
keeps the conversation's RunId and retained history/context, allocates a new TurnId, and applies the
approved turn-scoped reset. An active attempt still returns ExecutionBusy before any task
replacement or reset. Do not run two attempts on one context or silently interpret NewTurn as
steering. Stop the active attempt and wait for its cleanup before calling NewTurn. Restore accepted
state first if interruption, failure, release, or an uncertain save made that handle unusable. D121
still permits direct reuse after a clean accepted completion or limit pause.

For an idle unfinished turn, perform the following transition:

1. Validate the new task and setup before accepting it. Prepare required managed recovery for the
   previous turn, reusing accepted results and any saved After Recovery preparation under D61.
   Preserve pending-operation evidence and required native request groups. Do not execute queued
   business actions merely to finish the abandoned task or require another model answer from it.
2. Prepare the previous turn's canceled completion and the new task, identity, counter baselines,
   and reset LoopData. Keep prior facts in accepted history. Resetting custom data must not erase
   the evidence needed to inspect the previous task or its managed operations.
3. Accept previous-turn closure and new-turn admission/reset together in the normal NewTurn
   SaveBatch. The reset state is part of that saved transition, not an unsaved mutation after it.
   Preserve the initial save barrier before new model/tool work. Required recovery can have its
   existing separate After Recovery boundary; it does not itself close the old turn or admit input.
4. Execute the new task through the normal tools, Hooks, Subscribers, output, and limit paths.

Use the existing Finished/TerminationCanceled outcome for the replaced unfinished turn; do not add
another closure status or reason enum. Its explicit closure becomes the last completed result under
D74. The NewTurn call returns the new turn's result, not that previous canceled result. If there was
already a completed turn, starting the next task preserves that result as before. No final-answer
content is fabricated for a canceled task, and previous answers remain unchanged.

A failure before admission does not partially close the old task or accept part of the new task.
Reconcile a failed or uncertain save against complete stored batches. If storage accepted the
transition, restore that new task and Continue; do not submit it again. If it did not, the prior
accepted task remains available. Both replay paths must reproduce the same task identities, custom
data, history, counters, old-turn closure, and last completed result. External effects are not
undone; use the existing managed recovery evidence contract.

Continue remains the choice to resume the same accepted task, with the same TurnId and allowance.
NewTurn expresses a new task and therefore starts the approved turn accounting. Run counters and
run-scoped limits do not reset. A reached run limit can still pause the newly accepted turn before
new model/tool work. D111's same-task Continue path after a limit stop remains required.

Remove ExecutionNeedsContinue and its needs_continue value from the target API, tests, wire values,
and teaching examples. The remaining ExecutionStatus values are Finished, Paused, Busy, and
NoActiveTurn. Do not retain an unused enum value or invent a new rejection case to justify it. Apply
this rule to the execution interfaces and caller examples. D103's recovery of the same task still
uses Continue; this approval also permits explicit new input after restoring usable accepted state.
It does not make a failed mutable handle safe to execute directly.

Extend D63 verification with NewTurn on fresh, completed, paused, and restored unfinished contexts;
Busy without mutation or resource release; recovery reuse; and failure before/at/after the combined
save. Verify no half-transition after save failure or lost acknowledgment. Check both replay paths,
turn-reset versus run-counter preservation, no duplicate accepted task after recovery, the canceled
previous result through GetLastTurnResult, and no model/output processing to invent an old answer.
Keep the public flow small. If implementation reveals a concrete obstacle to this composition,
report the evidence rather than silently weaken the recovery guarantee or restore the old
restriction.

**Locked reusable execution context D121.** Keep ExecutionContext reusable after accepted turn
completion while its owner context is alive. This includes success, refusal, and accepted
cancellation. Allocate fresh attempt identities, cancellation scopes, and delivery workers for each
admitted execution attempt. Do not release the conversation handle merely because an answer was
accepted. Join attempt-owned producers, streams, and workers before the execution call returns. Keep
configured tool/model/recorder services and conversation state across turns; turn completion does
not destroy those services. Their own ownership and concurrency contracts still apply.

An accepted Stop or limit pause can reuse the context. Continue rechecks configured limits and
accepted usage. A fresh attempt does not grant another allowance. Changed Runner configuration still
requires a new Runner and restoration under D33/D34. Unexpected execution errors, worker
interruption, and failed or uncertain saves require accepted-state restoration before execution; do
not reuse provisional mutable state. Reconcile uncertain storage first. A released handle cannot
execute again. Apply the existing cannot-start checks before allocating an attempt.

During managed work, execCtx.Context() supplies the current attempt's Go context. Do not cache it
across turns. An old canceled Go context stays canceled. Preserve accepted RunId/TurnId/counters
under their existing contracts; NewTurn resets only its approved turn-scoped state. This locks
resource separation and reuse. D143 defines Stop without adding another public stop method.

**Locked final runtime cleanup D122.** Keep the existing signature:

```go
func (execCtx *ExecutionContext) Release()
```

Release is idempotent final cleanup. Prevent new execution on the handle, interrupt active work, and
wait for owned workers to finish. Release itself performs no terminal save and does not turn an
unfinished task into a canceled task. Accepted results remain available for read-only inspection
while the handle remains referenced. Runner cleans up each attempt without releasing the reusable
conversation handle after each accepted answer. The application normally defers Release.

Release can block while cooperative callbacks finish. Call it from the owner, not synchronously from
a Hook, tool, or Subscriber callback whose completion it must wait for. A callback can request a
pause through Stop without waiting on its own completion. D143 removes public Interrupt; Release
retains internal interruption. Disposal does not decide task completion.

**Locked single public Stop control D143, 2026-11-17.** The owner reaffirmed that Stop pauses
execution and that the caller can then use Continue or NewTurn. The owner rejected the additional
CancelTurn operation and requested removal of the superseded decisions and discussions. This is the
current stop contract, not a new open question for the main implementation thread.

```go
// Stop requests a pause of the current active execution attempt without completing its turn.
func (execCtx *ExecutionContext) Stop()
```

Stop is the one public operation for an intentional pause. Remove the existing public CancelRun and
Interrupt methods in the refactor; do not introduce CancelTurn or keep compatibility aliases. Keep
Go context cancellation/deadlines for worker interruption and Release for final cleanup. Release
remains the already approved method under D122, not another way to complete a task.

| Operation | Meaning | Next step |
| --- | --- | --- |
| Stop | Pause active work; keep the accepted task unfinished | Continue or NewTurn |
| Continue | Resume that task with its TurnId and usage | Ordinary execution |
| NewTurn | Close/replace an idle unfinished task and admit new input | Execute the new task |
| Release | Dispose of the runtime handle; do not complete its task | Restore to execute |

Stop is a concurrent-safe, nonblocking, idempotent request. It does not invoke the Loop or create a
second execution worker. The running NewTurn or Continue call owns cleanup, managed evidence, and
the recoverable pause save. Stop has no cause argument, completion result, or separate receipt. The
running execution call supplies the actual result after its owned work has ended.

On a successful user pause, return zero T, a non-nil ExecutionResult with Status = ExecutionPaused,
TerminationReason = TerminationStopped, and nil error. Add TerminationStopped = "stopped" to the
existing TerminationReason enum as shown in D123. Keep TerminationLimitExceeded for a limit pause.
Keep TerminationCanceled for completed task closure, including NewTurn replacing an unfinished turn.
Do not return Finished/Canceled from Stop or redefine a paused turn as a completed turn. This adds a
reason to the existing result, not another lifecycle enum or a second result interface.

Preserve the RunId, TurnId, accepted task, context/history, LoopData, counter baselines, counters,
and managed-operation/recovery evidence. Stop does not call ResetTurn, grant a fresh allowance,
erase prior work, or replace the last completed result. Accept a recoverable pause at the control
boundary used for stopped execution; both replay paths must restore the same logical state. Publish
no accepted answer merely because the pause completed. Continue clears the old attempt's stop
request, creates fresh attempt resources, and resumes the same task under its existing limits. An
accepted clean pause permits direct handle reuse under D121. D133 governs NewTurn replacement.

Stop selects the active attempt atomically when called. If there is no active attempt, including
fresh, paused, or completed state, it does nothing and must not affect a future attempt. Do not
cancel an idle task or require Stop followed by Continue merely to save a cancellation. A remote UI
command must be routed to its intended run/turn by the application. Serialize that validation with
application admission of new work and discard stale commands; a no-argument Stop cannot infer an
earlier UI target. Gent must never transfer a selected attempt's stop request to another one. This
routing rule does not add a public CancelTurn or an unreviewed request/receipt interface.

Stop prevents admission of more ordinary model/tool work and signals cancellation to in-flight work.
Join owned producers, streams, and delivery workers under D121/D129. Keep truthful tool and
native-context evidence. External effects are not undone. Preserve unfinished operations for the
managed recovery path; never mark them successful merely to finish a pause. A callback can call Stop
without waiting on itself; callbacks and providers must still cooperate with cancellation.

A Stop request does not prove persistence. Return Paused only after the pause save succeeds. A
failed or uncertain save retains the storage-error and reconciliation contract; unexpected code
failures retain D103. Restore accepted state before execution when the handle became unusable. An
unsaved stop request can be lost after a process failure; accepted history remains the authority.

If a terminal save was already fixed and submitted, it can win the race with Stop. Preserve any
accepted completion; do not rewrite that batch or substitute a stopped result. A completed turn
remains completed. NewTurn still reports Busy until the previous execution call releases ownership,
including its cleanup. Applications wait for that call before Continue or NewTurn.

Extend D63 verification with Stop during model/tool work, repeated Stop, Stop on idle state, Stop
racing with a terminal save, cleanup failure, and failed/uncertain pause saves. Verify direct reuse
after an accepted pause, full replay and RunStateSnapshot plus tail, stable task identity, no reset
of turn/run usage, preserved last completion, managed recovery reuse, and no accepted answer on
pause. Verify the next attempt does not inherit a previous stop request and the example UI rejects
stale commands. Apply the API removal and new reason consistently to code, codecs, tests, comments,
guides, and Quick Start during implementation. No runtime change is made here.

### Last completed result

GetLastTurnResult reads one stable accepted result without a model/tool call, output processing,
execution event, new attempt, or save. It remains available after Continue and after Release while
the handle is referenced. A valid T can itself be nil; only a nil result means absence. Check read
errors first. Missing codecs or incompatible saved output must not appear as no completed result;
use the approved load/restore compatibility checks. (D71/D74/D122)

| Saved state | Getter return |
| --- | --- |
| No completed turn | Zero T, nil result, nil error |
| Completed success | Saved T, saved result, nil error |
| Completed non-success | Zero T, saved result, nil error |
| Unexpected read/decode failure | Zero T, nil result, error |

Retain one last completed result in RunState and all earlier results in RunView. One retained result
bounds count, not byte size. Preserve its typed value, delivery content, TurnId, and required media.
A normal next-turn start, pause, interruption, rejection, or compaction preserves it. Accepted
completion replaces it atomically. D133's replacement of an unfinished task records that task's
Finished/Canceled completion and makes it the last result in the same new-admission save.

The getter may read while another turn runs. Read metadata, TurnId, and T from the same stable
accepted record so concurrent completion cannot mix turns. Never mutate a previously returned
result. Paused/Stopped and Paused/LimitExceeded do not replace the last completed result. A
completed cancellation is distinct from D103 failure, which creates no completed error result.
(D74/D111/D143)

## Unexpected execution failures

**Locked error pattern D103, 2026-11-16: Propagate, Fix, Restore, Continue.** The owner approved the
error and retry proposal, then required it to be a named design pattern. Errors caused by execution
code must reach the application with enough detail to diagnose the failure. The application can log
the error, fix its code or configuration, restore accepted state, and call Continue. A code error
ends the current attempt; it does not accept a completed turn.

This rule covers unexpected errors propagated from execution Hooks, Loop.Next, output processing,
and managed request construction or validation. An invalid request or unusable native context caused
by the selected code/configuration uses non-nil error, including unusable retained native context.
Gent must not turn these failures into an expected Invalid/Incompatible call result, ordinary model
steering, or an automatic retry of the unchanged failing operation.

| Concern | Locked behavior |
| --- | --- |
| NewTurn / Continue return | Zero T, nil ExecutionResult, non-nil error |
| Failed operation | Stop; do not send a request rejected by local validation |
| Attempt resources | Cancel and release owned workers through normal cleanup |
| Accepted unfinished turn | Keep it unfinished and available to restored Continue |
| Accepted SaveBatch values | Preserve their evidence, state, counters, and order |
| Failed provisional work | Do not accept it merely to save a completed error result |
| Terminal save | Do not make one solely because this code error occurred |
| Last completed result | Preserve it under D74; do not replace it with the failed attempt |
| Retry | Fix the cause, restore the latest accepted state, then Continue |

The normal return tuple and the four approved ExecutionStatus values remain unchanged. There is no
ExecutionFinished result for this exceptional return. GetLastTurnResult still reads only the last
accepted completed turn. If NewTurn already accepted the task, retry with Continue rather than
submitting that task again. This is same-task recovery; D133 also permits explicit new input through
NewTurn on restored idle state, closing the previous task. If setup failed before any initial turn
was accepted, use the ordinary NewTurn path after correcting setup.

Restore through either full batch replay or RunStateSnapshot plus the accepted tail. Both paths must
recover the same accepted state under D53/D65. Do not resume from the failed worker's partly changed
memory or accept broken provisional LoopData to report the error. If an accepted savepoint contains
an unfinished managed tool operation, use the normal recovery contract for that operation. This
preserves retryability; it does not make unchanged bad code succeed, undo external effects, or
reopen an already completed turn.

**Locked diagnostic responsibility under D103.** Gent must surface as much relevant detail as is
available in the returned error so the application can identify the failing code and correct it:

- Preserve the original cause when adding context. Use Go error wrapping so errors.Is/errors.As
  still work; do not flatten the cause into an unrelated generic message.
- Identify the failing stage and operation, such as BeforeModelCall, BeforeToolCall, Loop.Next,
  Output.Process, or native request construction. Add component/callback names when available.
- Include the available run, turn, context, iteration, and logical operation identities needed to
  locate the failure. Include provider/model or tool identity when it explains that operation.
- Explain the failed requirement and the relevant actual/required values or request location, when
  known. Do not return only "model call failed" when the lower layer has useful details.
- Retain available source or stack detail supplied by the original error. Do not invent a code
  location or assume that an ordinary Go error contains a stack trace.
- Preserve the main error if cleanup or diagnostic recording also fails; do not replace or silently
  discard either failure. Return enough context to distinguish their operations.

Raw troubleshooting detail belongs in the application error/private diagnostics, not model steering
or public chat content. The application owns logging. Diagnostic reporting must not accept broken
provisional execution state or add a savepoint outside the approved schedule. Exact message
formatting, internal helpers, and any general panic policy remain implementation choices; this
approval does not require a public error hierarchy or a panic-recovery API.

**Scope boundaries.** Expected lifecycle and load/replay conditions retain their typed results.
Normal output rejection remains a typed decision with steering. Tool.Call/Tool.Recover errors
already handled by the managed tool contract remain operation failures with safe steering and
private diagnostics; this rule does not turn every tool return error into a run failure. A Hook
callback error is distinct from a handled Tool.Call error and must propagate when it prevents the
dependent operation. Subscriber failures keep their isolated delivery contract. Cancellation,
limits, and storage failures retain their separate contracts. D72/D73's rules for repeated custom
work and callback side effects remain in force.

**Required verification and documentation.** Extend D63's implementation checks to cover:

- A failed Hook blocks its model/tool action and returns the original cause with operation context.
- Invalid or incompatible request construction fails before the rejected model request is sent.
- A custom Loop.Next error after an accepted savepoint leaves the turn unfinished, returns no
  ExecutionResult, and preserves the previous completed result and accepted custom/framework state.
- After the cause is fixed, full replay and RunStateSnapshot plus tail both permit Continue to
  finish the same accepted turn without submitting its task twice or repeating accepted managed work
  for the same operation. Repeated failures must not create terminal error results.
- Root error inspection and contextual diagnostics survive propagation and cleanup. Separate tool,
  Subscriber, cancellation, limit, and storage contracts retain their established behavior.

Teach this named pattern in the relevant Runner, Loop, and Hook Go documentation and in the Quick
Start during implementation. Show the error return, application logging/fix, restore, and Continue
flow. This approval updates requirements only; it does not authorize implementation in this turn.

Do not add ModelRequestCheck, ModelCallStartResult, TerminationModelRequestRejected, or a separate
Model.ValidateRequest API for these errors. Check the effective input after Hooks in the existing
construction/validation path. Preserve usable native evidence. Automatic native-state conversion or
reset is outside this feature. Fix normal same-provider continuation mappings and verify them;
report a concrete unsupported case rather than silently losing accepted evidence. (D103/D119)

## Shared configuration

**Shared Config and RecordingConfig — D114/D116.** Use these complete declarations:

```go
type Config struct {
    ToolChain          ToolChain
    Recorder           Recorder
    Codecs             *CodecRegistry
    Subscribers        []*SubscriberRegistration
    Hooks              *Hooks
    Limits             []Limit
    Recording          *RecordingConfig
    SteeringMessages   *SteeringMessages
    CompactionTrigger  CompactionTrigger
    CompactionStrategy CompactionStrategy
    NewLoopData        func() LoopData
}

type RecordingConfig struct {
    MaxPendingEvents int
    MaxPendingBytes  int
    SaveTimeout      time.Duration
}
```

| Setting | Locked behavior when omitted or configured as shown |
| --- | --- |
| ToolChain | No default tools |
| Recorder | In-memory acceptance; no durable recovery or supported transfer |
| Codecs | A built-in registry for the root run |
| Subscribers / Hooks | No consumers / no mutation callbacks |
| Limits nil or empty | No execution-limit rules |
| Recording | 65,536 pending events; 64 MiB encoded events; 30-second Save timeout |
| SteeringMessages | Existing framework defaults |
| Compaction | Enabled when both members exist |
| Both compaction members absent | Disabled |
| Only one compaction member present | Invalid configuration |
| NewLoopData | BasicLoopData allocation for fresh contexts |

**Explicit limits — Locked D114.** Reuse the existing helper; there is no automatic call to it:

```go
// DefaultLimits returns a standard limit preset for explicit caller configuration.
func DefaultLimits() []Limit
```

Each call supplies an independent slice that the caller can change before configuring a Runner. A
supplied slice replaces the complete local rule set. To change one preset rule while keeping the
others, edit that returned rule. Appending a rule adds another rule; it does not replace an existing
one. Do all configuration before use under D33.

```go
// Both configurations have no local execution-limit rules.
withoutLimits := &gent.Config{}
emptyLimits := &gent.Config{Limits: []gent.Limit{}}

// Opt in to the standard preset explicitly.
standardLimits := &gent.Config{Limits: gent.DefaultLimits()}

// This config has only this local rule; no standard-preset rules are added.
onlyIterations := &gent.Config{
    Limits: []gent.Limit{
        {
            Type:     gent.LimitExactKey,
            Key:      gent.SCIterations,
            MaxValue: 20,
        },
    },
}
```

Advertise the explicit preset in the Quick Start and configuration guide, together with the one-rule
replacement example. No-limit configuration does not disable Recording bounds, Subscriber delivery
bounds or caller-context cancellation.

Resolve defaults without mutating caller configuration. Required Config/Loop parameters are not
optional; callers use &Config{} for defaults. Zero Recording fields select defaults; negative values
are invalid. Validate known setup before dependent work through the approved preparation/execution
methods, not a competing Config.Validate API. Unknown custom factory/callback failures propagate.
Configuration is fixed while used. Runtime progress belongs to contexts, not shared Config fields.

MaxPendingEvents and MaxPendingBytes are hard allowances for unsaved numbered event evidence. They
never trigger an extra progress save. Before queueing an event that would exceed a bound, stop the
attempt with a detailed recording/configuration error, cancel and join producers, and retain the
last accepted state. Do not number/deliver that rejected event or make a terminal error save. D103's
fix-and-restore pattern applies: increase the allowance or reduce event volume, then Continue. A
completed external tool effect still follows the managed recovery contract on the next attempt.

An event is never split or silently omitted to fit. Account for its serialized size before enqueue;
streaming encoders may stop counting once the allowance is exceeded. These are pending-event bounds,
not a claim to bound model SDK objects, custom LoopData, complete checkpoint values, or the whole
process heap. There is no automatic disk spool in this feature. Live delivery buffers are separate
and do not replace persistence buffers.

Use SaveTimeout for the recorder call with cancellation independent of an interrupted model call.
The recorder must honor its context and return; Gent waits for the actual result. A timeout does not
prove that storage rejected the batch. Preserve the fixed BatchId/content for any storage retry and
reconcile ambiguous acceptance before another worker writes.

Tradeoff: a very large single operation can require a larger configured bound and repeat unsaved
model work after failure. This keeps the approved savepoint schedule, avoids unbounded pending
queues, and avoids a second spool/storage API. The numeric defaults are approved initial settings to
verify, not measured guarantees about every provider workload.

Keep the Runner free of per-run mutable progress. Pursue concurrent reuse with distinct contexts
when it is simple and reliable. Do not add an implicit current context, broad execution lock, worker
pool, or machinery that tries to make arbitrary custom dependencies safe. Shared loops and services
keep their own concurrency contracts. Report a concrete complexity or reliability obstacle before
expanding this conditional reuse direction. (D45)

## SaveBatch, RunStateSnapshot, and replay

Gent owns format versioning, integrity metadata, encoding, decoding, state validation, and replay
through CodecRegistry. The application selects storage and capture cadence, implements
Recorder.Save, and stores complete encoded batches and optional RunStateSnapshots. It must not need
to construct framework state, walk runtime objects, or implement a replay validator. Reuse
registered codecs for custom/provider data. (D40/D46/D79)

```go
// Recorder persists a fixed batch and can capture its matching RunStateSnapshot.
type Recorder interface {
    Save(
        ctx context.Context, batch *SaveBatch,
        captureRunStateSnapshot func() (*RunState, error),
    ) error
}
```

Prepare the consistent SaveBatch and its source state before Save. Dependent work waits for
acceptance. The callback captures the prepared state through that exact batch, including custom
state and framework continuation; its cursor is the batch's final event sequence number. It must not
read later mutable fields or serialize custom state again after concurrent work changes it. Every
approved savepoint is recoverable; no diagnostic-only save is a second recovery contract.
(D40/D52/D53/D54/D62)

Keep full RunStateSnapshot capture lazy. Required batch/custom-state capture still happens when the
recorder does not request a RunStateSnapshot. Call the callback only while Save is active; never
retain it for asynchronous use or let it reenter acceptance. The returned stable RunState can
outlive Save and can be encoded asynchronously under its ownership contract. No public context
capture or transfer method is needed. Same-process transfer also uses a Recorder. (D40/D64)

A captured value is a candidate until its batch is accepted. Store batch and RunStateSnapshot
atomically where possible, or commit the batch before publishing the RunStateSnapshot. Never publish
a RunStateSnapshot ahead of durable evidence. A later RunStateSnapshot publication failure must not
claim an already committed batch was rejected. Storage retries use the same fixed BatchId and
content; reconcile a lost acknowledgment before another writer continues.

The application owns transactions, one-writer fencing, exact-retry evidence, accepted-head reads,
and byte storage. Capture cadence is application policy. An execution without a Recorder can run
with in-memory acceptance but has no supported saved-state transfer or crash recovery. A memory
Recorder supports same-process restoration; persistent storage is needed for crash durability.

Capture cost must scale with retained execution state, not the full RunView archive or replay of all
old batches. Stable capture can require copies, while immutable sharing can reduce work under
explicit ownership. Encoding, bytes written, allocations, and save delay need separate measurements.
Large custom data or active media can dominate cost. Do not treat measurements of the old complete
RunView as measurements of this retained-state design. (D40/D78/D119)

### Approved savepoints

**Save schedule — D54/D61/D111/D143.** Each row captures complete logical state, including LoopData.
These are reasons to save, not a requirement to issue separate writes at adjacent equivalent points.
Under D72, resume position means Gent-owned continuation state and any optional custom control
fields. It does not require an instruction pointer or step enum for arbitrary custom Next code.

| Boundary | State that must be accepted before dependent work |
| --- | --- |
| Initial execution or NewTurn | Input, turn identity, initial data, and resume position |
| Before iteration | Iteration state and the matching loop entry position |
| After iteration | Updated data, history, counters, and next control position |
| After compaction | Summary, retained messages, selection, and compaction decision |
| Before each managed tool call | Chosen plan, call identity, effective input, and resume position |
| After each managed tool call | Result or safe failure, updated plan/data, and recovery evidence |
| After Recovery | Reports/messages, completed recovery state, and matching resume position |
| Stop or limit pause | Unfinished task, usage, custom data, and managed recovery evidence |
| Terminal finish | Outcome, accepted output, final counters, and custom data for NewTurn |

The before-tool state must also preserve the model response and native continuation that selected
the calls. Save the plan through this boundary instead of requiring a separate write merely to
create it. The after-tool category includes new recovery reports before the next dependent model
call. Programmatic tool calls still need identities and recovery data for their inner effects; an
outer run_code checkpoint alone cannot account for completed inner actions.

Coalesce initial and first-iteration preparation, or iteration completion and terminal finish, only
when their combined state preserves the required resume semantics. Coalesce adjacent iteration
boundaries only when no intervening work needs a separate durable state. Do not merge away a save
that must precede an external effect.

**Locked model-work tradeoff D57, 2026-11-13.** Accept repetition of unsaved model work to reduce
storage roundtrips. Do not add a model-response savepoint by default. Preserve accepted counters and
enforce limits against observed usage during execution; save frequency must not control limit
checks. Replay must not count saved usage again. Document that interruption can leave provider token
charges absent from Gent's recovered counters, including usage reported after the last savepoint.
Retries can add further charges. Counters and token limits cannot guarantee an exact billing total
or a hard provider-spend cap across interruptions when usage was never observed or accepted.

Do not retain the old default schedule of separate model-request/response progress saves. Queue and
freeze their evidence, then accept it at the approved boundary before dependent effects. Accepted
delivery observes those saves; Live delivery can expose provisional evidence earlier. D114's
pending-evidence bounds do not add savepoints. A save retry reuses the prepared transition; it must
not repeat ResetTurn or admit input twice. (D54/D57/D84/D114)

### Replay and load interfaces

**Replay declarations — D46/D66/D79/D112/D126.** Detail is diagnostic text. Callers branch on Status
and structured cursor fields. The byte layout is a private implementation choice:

```go
func NewRunState(runId string) *RunState

func (codecs *CodecRegistry) MarshalRunState(state *RunState) ([]byte, error)
func (codecs *CodecRegistry) UnmarshalRunState(data []byte) (*RunStateLoadResult, error)
func (state *RunState) RunId() string
func (state *RunState) LastAppliedEventSequenceNumber() uint64
func (state *RunState) Apply(
    batch *SaveBatch, codecs *CodecRegistry,
) (*ApplyStateResult, error)

type RunStateLoadStatus string

const (
    // RunStateReady means the decoded state is valid and available in State.
    RunStateReady RunStateLoadStatus = "ready"

    // RunStateInvalid means the saved representation is corrupt or inconsistent.
    RunStateInvalid RunStateLoadStatus = "invalid"

    // RunStateIncompatible means a required format or codec is unavailable or unsupported.
    RunStateIncompatible RunStateLoadStatus = "incompatible"
)

type RunStateLoadResult struct {
    Status RunStateLoadStatus
    State  *RunState
    Detail string
}

type RestoreStatus string

const (
    // RestoreReady means Context owns the restored state and is ready for execution.
    RestoreReady RestoreStatus = "ready"

    // RestoreInvalid means the state is incomplete or inconsistent for execution.
    RestoreInvalid RestoreStatus = "invalid"

    // RestoreIncompatible means required current component bindings cannot restore the state.
    RestoreIncompatible RestoreStatus = "incompatible"
)

type RestoreResult struct {
    Status  RestoreStatus
    Context *ExecutionContext
    Detail  string
}

type ApplyStateStatus string

const (
    // ApplyApplied means the complete batch was accepted into the replay target.
    ApplyApplied ApplyStateStatus = "applied"

    // ApplyDuplicate means the exact batch already has a known accepted receipt.
    ApplyDuplicate ApplyStateStatus = "duplicate"

    // ApplyCursorConflict means the batch cannot follow the current accepted cursor.
    ApplyCursorConflict ApplyStateStatus = "cursor_conflict"

    // ApplyReceiptRequired means storage must prove an older batch was already accepted.
    ApplyReceiptRequired ApplyStateStatus = "receipt_required"

    // ApplyInvalid means the batch is malformed, has a bad digest, or belongs to another run.
    ApplyInvalid ApplyStateStatus = "invalid"

    // ApplyIncompatible means required format or codec support is unavailable.
    ApplyIncompatible ApplyStateStatus = "incompatible"
)

type ApplyStateResult struct {
    Status                              ApplyStateStatus
    Detail                              string
    ExpectedPreviousEventSequenceNumber uint64
    ReceivedPreviousEventSequenceNumber uint64
}
```

NewRunState creates an empty replay target at cursor zero with the supplied RunId. D126 allows an
empty ID at construction without a panic. Apply rejects empty target/batch IDs and mismatches with
ApplyInvalid, detailed text, and nil error. It does not assign an ID from the batch. Apply the
initial batch before Restore; a target without accepted initial state returns RestoreInvalid. An
empty run ID also gives RestoreInvalid on Restore and RunStateInvalid on decode under D126. The
constructor does not create a fresh execution or an extra submission path. State/Context is non-nil
only for the corresponding Ready result. Expected results have nil error; unexpected operation
failures return nil result and an error. Detail explains the result and is not parsed.

Cursor details are meaningful only for ApplyCursorConflict. An earlier exact known receipt returns
Duplicate. A wholly older unknown batch returns ReceiptRequired rather than being assumed equal. An
overlapping batch that extends past the cursor is a conflict, not an older duplicate. Retain the
most recent receipt in compact state; do not retain an unbounded receipt map merely to accelerate
execution. The application resolves older receipts in storage, verifies the exact BatchId/digest,
and skips only a proven duplicate. No extra receipt-store interface or Apply overload is proposed.

Framework-known structural/schema/version failures use the typed results. A custom codec or custom
restore callback that returns an operation error keeps that wrapped error; do not classify it by its
text. Invalid current Config is a setup error. Unsupported saved component data retains the typed
incompatibility result. Restore checks saved values without requiring a matching Runner version.

Preserve D78 ownership: successful Apply transfers retained values, duplicate/rejected Apply changes
neither input, successful Restore transfers state to the context, and failed Restore keeps caller
ownership. Validate before committing a state transition. Do not implement this by copying the full
history on every Apply. Tradeoff: explicit status cases require a switch, but identify the recovery
action without error matching. One Incompatible result avoids a redundant Unsupported state.

**Locked replay-constructor identity validation D126, 2026-11-17.** The owner approved the empty-ID
proposal with "Okay please lock this in then." NewRunState and NewRunView accept an empty run ID
without a panic. Keep their approved signatures; validate identity when the target is used. Use the
same identity rules for execution-state and archive replay.

These public constructors create empty replay targets. NewRunState supports batch-only execution
state replay; NewRunView supports archive replay. Fresh execution uses NewExecutionContext and
NewTurn. Loading a RunStateSnapshot restores its saved identity through UnmarshalRunState; it does
not need a new replay target or a separately supplied identity for Restore.

- NewRunState("") and NewRunView("") return empty targets without panicking for that input. Creating
  such a target does not accept execution state or start work.
- RunState.Apply and RunView.Apply require a non-empty target RunId and a non-empty batch RunId.
  Validate both independently; two empty IDs must not pass merely because they match. Non-empty IDs
  must match. These identity failures return ApplyInvalid with nil error under D112.
- Detail must explain whether the target ID is empty, the batch ID is empty, or the IDs differ.
  Include the available expected and received identities where useful. Callers branch on Status, not
  the wording. No exact diagnostic string is fixed by this approval.
- Reject the whole batch without changing the target, its cursor, or the supplied batch. No partial
  application or ownership transfer occurs on rejection. Preserve D78's existing contract.
- Direct Restore of a RunState with an empty ID returns RestoreInvalid, nil Context, and nil error.
  Decoding an otherwise readable saved RunState with an empty ID returns RunStateInvalid with nil
  State and nil error; it must not return RunStateReady. Give detailed diagnostic text in both
  results. Unexpected codec/operation failures keep D112's separate error contract.
- Do not generate an ID, infer it from the first batch, or replace a target's ID to make replay fit.
  The application corrects its input and creates a new target with the intended run ID. Recovery
  preserves the saved run identity.

The tradeoff is that the caller's mistake is reported at first use rather than construction. An
empty replay target has no accepted work, so the existing explicit validation results handle this
without a new constructor, result type, or panic policy. This approval supersedes the earlier
empty-ID panic recommendation. It does not change required-pointer contracts.

Extend D63's future checks for both replay targets: empty target ID, empty batch ID, both empty,
non-empty mismatch, and matching valid IDs. Check detailed results, unchanged rejected inputs and
cursors, and no automatic identity assignment. Cover direct Restore and decoded saved state with an
empty ID. Document the same rules on the constructors, replay/load APIs, and Quick Start.

**Locked archive replay API D118, 2026-11-17.** The owner approved response item 9, "Archive replay:
align its API with execution-state replay," with "Accepted. Let's lock this in." Use the following
declarations for full accepted-history replay:

```go
func NewRunView(runId string) *RunView
func (view *RunView) Apply(
    batch *SaveBatch, codecs *CodecRegistry,
) (*ApplyStateResult, error)
```

Replace the value-returning ApplyBatch entry point with RunView.Apply; do not keep both public ways
to apply a batch to that view. The constructor initializes Gent's format metadata. Use
ApplyStateResult, including Detail for explanatory text and typed Status for caller decisions. Keep
D78's ownership and no-partial-application rules for retained batch values.

RunView and RunState share validation/transition rules with different retained projections. RunView
retains the complete accepted archive and receipt index. RunState retains the evidence needed to
continue execution and bounded recent receipts. Application storage pages complete encoded batches;
Gent decodes and applies them without running models, tools, or output processing. No additional
archive-store, pager, or decoder interface is required for this feature. Full archive construction
intentionally uses memory proportional to the retained archive; normal execution restoration does
not construct that view.

### Loading flow

Select one accepted head H. If storage has a compatible RunStateSnapshot at or before H, decode it
with UnmarshalRunState, check RunStateReady, run identity, and cursor. Otherwise, start with
NewRunState for full batch replay. Apply complete accepted batches after the loaded cursor through
H, reading pages when needed. Check Apply results and the final cursor before Restore. Do not skip
an unproven old batch: ApplyReceiptRequired requires exact accepted identity evidence from storage.
Do not use a compact execution state as proof that an arbitrary older batch was accepted. (D46/D112)

Restore returns the typed result without running work. Check RestoreReady, retain its Context, and
defer Release. Continue executes the accepted unfinished task; NewTurn submits explicit new input on
an idle context under D133. On an uncertain admission acknowledgment, load the accepted head: if the
input was accepted, Continue it instead of submitting it again. Application fetch and HTTP
request-deduplication helpers are not new Gent interfaces. (D39/D46/D133)

## Custom LoopData and managed recovery

LoopData is per-run mutable logical state. Capture all its recoverable values, including custom
fields, with matching framework continuation at every approved savepoint. Batches must carry this
state without relying on optional RunStateSnapshots. Runtime clients and Go contexts are bindings,
not persisted logical values. Missing bindings or incompatible saved data produce an explicit
restore result, never empty BasicLoopData fallback. (D41/D42/D48/D62)

```go
type CheckpointState interface {
    CheckpointType() string
    CheckpointVersion() int
    SaveCheckpointState() (*EncodedValue, error)
    RestoreCheckpointState(state *EncodedValue) error
}

type TurnResetter interface {
    ResetTurn()
}

type LoopData interface {
    CheckpointState
    TurnResetter
}
```

CheckpointType and CheckpointVersion identify the component's saved contract, not a Runner version.
SaveCheckpointState supplies stable owned data. RestoreCheckpointState borrows its input and
restores the fresh factory instance. Config.NewLoopData func() LoopData is a pure, task-independent
factory with no model/tool calls or external setup. It defaults to BasicLoopData for fresh contexts.
Restore allocates and restores custom data even after turn completion; later NewTurn keeps that
logical instance. (D76/D83/D120)

ResetTurn is required on LoopData only. Do not apply it to tools, models, Subscribers, Hooks, or
other checkpoint components. NewTurn invokes it, including for the first task; Restore and Continue
never do. Reset only author-declared turn fields and preserve run fields. Accept the new task and
reset data together. Failed admission must not overwrite accepted saved values; restore unusable
provisional state before retry. ResetTurn has no error return and adds no general panic policy.
(D43/D48/D84/D120/D133)

Keep ResetTurn limited to local assignments to those fields. It must not call models or tools, do
external I/O, or perform other external work. Task validation and managed external preparation
belong outside ResetTurn. Local assignment needs no external-operation failure or recovery protocol.
An external action before accepted admission could repeat after a failed admission and restoration;
the initial save barrier cannot undo that action. (D84/D120/D133)

Direct field assignment does not make data durable or create automatic mutation tracking. Keep
changes and matching control state within the managed save contract. Do not add a save per field,
public update registry, or custom-data object that writes its own Recorder. Embedding BasicLoopData
cannot discover or serialize added application fields; a promoted serializer may save only the
embedded base. Custom authors must implement complete capture/restore and any added turn reset.
(D42/D62/D76/D84/D119)

**Locked LoopData evolution documentation D76, 2026-11-16.** The owner approved documenting
backward-compatible evolution for custom LoopData implementations at the LoopData interface in
[agent.go](agent.go), and explicitly requested that this approval be recorded in this requirements
document. This is required implementation documentation; this review does not edit the Go source.

The documentation must guide custom loop authors to evolve saved data through compatible additions.
Preserve existing saved field identities, types, units, and meanings. Add a new field for a changed
representation or meaning, and deprecate the old field while retaining the data and reader behavior
that older saved runs still need. New fields must support older saves through valid defaults or
existing saved information. Distinguish absent data from an explicit zero value when that difference
matters. Do not silently reset accepted progress when a new field is absent.

If an author uses saved steps or enum values, preserve their identities and meanings, and add new
values for new behavior. This guidance does not require a step enum; D72's custom-loop restart scope
remains unchanged. Keep saved type identities and compatibility versions stable for compatible
additions. Document the chosen codec's naming rules; current reflective encoding uses Go field
names, so a JSON tag alone does not preserve a renamed field.

Do not add the proposed CheckpointStateMigrator interface or its migration result types for
LoopData. Ordinary custom-state restoration remains responsible for reading compatible older data.
Preserve explicit invalid/unsupported outcomes when data cannot be used; never substitute empty
state. The reason is to keep the extension surface small and guide authors toward compatible
saved-data changes without another conversion protocol to implement and maintain.

This requirement concerns custom LoopData's persisted contract, its serializers, and the code that
uses restored values. It does not require public Gent Go APIs to remain backward compatible during
development, settle framework-format or provider compatibility, or promise that older code can read
newer saves. The implementation must document this guidance directly on LoopData rather than leave
it only in this design record. D83/D84 later lock the factory and turn-reset APIs.

**Locked custom-loop restart scope D72, 2026-11-16.** The owner accepted this contract: Gent
restores accepted framework state and custom LoopData. Continue completes the required managed
recovery, then enters Loop.Next from its beginning. Custom loop authors are responsible for making
their own code safe to restart. Gent does not require a step enum or provide automatic continuation
within arbitrary custom Go code. This supersedes D56's mandatory custom resume-position requirement
and the earlier recommendation to introduce required custom steps or a workflow abstraction.

The owner explicitly accepts duplicated work during continuation and different model responses when
a model call repeats. The reference loop's existing recovery architecture is sufficient for this
direction; no additional public step mechanism is required. Gent retains internal framework and
reference-loop bookkeeping, including accepted history, counters, operation identities, saved tool
plans, and accepted recovery preparation. Reuse accepted results for the same managed operation; do
not treat permission to repeat computation as permission to replay a completed tool effect. Unsaved
model work can repeat under D57. Accepted model/tool evidence still remains saved evidence.

Full batch replay and RunStateSnapshot-plus-tail replay must restore the same accepted state under
D53/D62/D65. That guarantee does not promise identical subsequent custom control flow or model
output. New calls with new identities are new operations, even if their business purpose matches an
earlier call. Custom authors can use ordinary state checks, repeat-safe operations, or optional
saved steps when needed. The reference loop owns its internal recovery behavior without requiring
applications to implement those steps. After Recovery preparation and reuse under D61 remain Locked.

This scope applies to continuation after restoration; Restore itself still only reconstructs state
and prepares resources. It does not execute custom work or call a model. Existing gaps against the
new savepoint/multi-turn design and D63's implementation tests remain separate work. Approval of
this simpler contract does not certify that every proposed API or savepoint is already implemented.

**Locked callback retry caveat D73, 2026-11-16.** The owner accepted that custom Output.Process,
validators, and hooks can repeat after interruption and requested this caveat in the reference
loop's code documentation. An external write inside Output.Process can complete before the terminal
output is durably accepted. A resumed attempt can call Process again and repeat that write. Managed
tool recovery does not automatically cover external effects inside such callbacks. Keep them safe to
repeat or use a managed tool for an action that needs its recovery contract. Once terminal output is
durably accepted, restore reuses it without calling Output.Process again.

Keep this caveat in [agents/react/doc.go](agents/react/doc.go) and custom-loop documentation.
Ordinary built-in answer parsing and validation do not require a new application step interface. D73
does not weaken the accepted-output publication barrier or authorize automatic repetition of
accepted managed tool actions.

**Locked recovery orchestration D61, 2026-11-13; custom-loop scope revised by D72.** The owner
approved Gent-owned recovery followed by the Loop's normal result-processing path. Restore only
reconstructs state and prepares resources. Continue performs required recovery preparation before
Loop.Next, reuses accepted results, checks unfinished managed calls, and prepares the recovery
results/messages and matching continuation state. Accept that state at the After Recovery savepoint
through the existing Recorder.Save before entering the Loop. No second persistence hook or public
Recover alias is needed. D72 requires no custom step interface; custom data remains serializable.

After Recovery is now an approved savepoint. Its save failure blocks Loop.Next and model calls.
Unresolved is valid accepted evidence; the savepoint means preparation is complete, not that all
external outcomes are known. Avoid an extra write when nothing changed, and combine it with the
final recovered-tool save when both describe the same complete prepared state.

An interruption after acceptance must restore those recovered messages, evidence, plan state,
accounting, custom data, and framework continuation state from batches or a RunStateSnapshot plus
tail. Continue then enters Loop.Next without repeating the accepted recovery checks, rebuilding
replies, or issuing another After Recovery save for the same state. An accepted Unresolved report
also skips that check. Use durable completion state to decide, rather than the new process/attempt
identity. Later accepted work can introduce a new unfinished operation that needs recovery.
Completion is not a permanent per-run flag. Savepoint layout, internal helpers, and the
representation of completion are implementation choices under this contract. The orchestration,
placement, reuse, and save contract are Locked.

D133 also requires this recovery preparation before NewTurn replaces an idle unfinished task. It
does not run queued business actions to complete the old task. Reuse any accepted recovery
preparation. Apply current limits and cancellation to recovery; a completed Unresolved report is
accepted preparation, not permission to rerun the old action or its accepted recovery check.

**Accepted recovery survives another interruption.** The After Recovery savepoint must include the
recovery evidence, prepared messages and active selection, operation identities, plan/completion
state, counters, custom data, and the matching resume position. These must be reconstructable from
accepted batches even if the application never requests a RunStateSnapshot.
RunStateSnapshot-plus-tail replay must produce the same prepared state. A marker without the state
it certifies is insufficient.

```text
First Continue
    -> prepare recovery results/messages
    -> Recorder.Save accepts After Recovery
    -> process stops before Loop.Next

Restore accepted state
    -> reconstruct the saved results/messages and completion state

Second Continue
    -> enter Loop.Next with the accepted prepared state
    -> no repeated recovery check or After Recovery write for that accepted preparation
```

Do not interpret an accepted Unresolved report as unfinished preparation. It is a completed check
whose evidence tells the model the outcome remains unknown. Later model-directed checks are new
managed operations. Similarly, a fresh AttemptId alone must not invalidate accepted preparation. Tie
completion to the saved continuation and operation identities, not a process-local flag or a
permanent conversation-wide boolean. A later tool-ready savepoint can introduce new recovery work.

Before an After Recovery save is accepted, another interruption can require repeating unsaved
checks. If storage committed but its acknowledgment was lost, resolve the accepted storage head
under the existing save/retry contract before deciding what to repeat. Never assume an error proves
the preparation was not saved. Continue must still honor cancellation and limits when it skips
already completed recovery.

Implementation verification must cover both batch-only and RunStateSnapshot-plus-tail reconstruction
for:

| Interruption point or state | Required continuation behavior |
| --- | --- |
| Before preparation is accepted | Check only work with no accepted recovery result |
| Accepted preparation; before Loop.Next | Reuse prepared state; enter Next with restored data |
| Saved report is Unresolved | Reuse that report; do not automatically repeat its check |
| Recovery save fails or acceptance is unknown | Block the Loop until acceptance is established |
| Later saved work has a new unfinished call | Recover that new work; retain earlier evidence |

Verify exact messages, selection, custom fields, counters, and call identities, and assert that no
original action, accepted recovery check, or prepared reply is repeated. These are implementation
requirements, not evidence that the refactor already passes them.

### Tool recovery example

Assume A has a saved success, B has a saved call-error response, and C has a saved identity/input
but no accepted result. Restore reconstructs state without calling tools. During required recovery:

| Call | Action |
| --- | --- |
| A | Reuse the accepted response; do not call Call or Recover |
| B | Reuse the accepted safe failure guidance; do not call Call or Recover |
| C | Reuse a saved result if one exists; otherwise call Recover for its saved identity/input |

Recover reports Completed, NotStarted, or Unresolved. Completed preserves the original response and
warns if it can be stale. NotStarted does not automatically retry Call. Unresolved tells the model
to check state before repeating an action. A missing or failed handler supplies safe recovery
guidance. A saved tool error does not prove no external effect occurred. None of these statuses
permits automatically repeating the original action. Keep correlated native tool replies and
required request groups. The custom Loop remains responsible for using the saved context in its own
request. (D60/D61/D72; existing managed-tool contract)

Use managed plans and execution, including PrepareCalls and ExecuteNext, for recoverable tools.
Direct Tool.Call or unrelated external writes bypass that protection. Programmatic tool execution
must retain inner-call identities/evidence; one outer run_code record cannot describe all inner
effects. Document these limits in the API and custom-loop Quick Start. (D54/D60)

## Statistics and limits

**Statistics — D111/D114/D116/D117.** GetCounter remains cumulative. Add the turn getter and Scope
field below. Configure execution limits explicitly; hierarchy-only counters are removed:

```go
type LimitScope string

const (
    // LimitTurn measures counters within the current turn across all continued attempts.
    LimitTurn LimitScope = "turn"

    // LimitRun measures counters across all turns of the run.
    LimitRun LimitScope = "run"
)

type Limit struct {
    Type     LimitType
    Key      StatKey
    MaxValue float64
    Scope    LimitScope
}

func (stats *ExecutionStats) GetCounter(key StatKey) int64
func (stats *ExecutionStats) GetTurnCounter(key StatKey) int64
func (stats *ExecutionStats) GetGauge(key StatKey) float64
```

An empty Scope selects LimitTurn. Save counter baselines/attribution at the accepted NewTurn
boundary. Continue and Restore preserve those values. GetCounter remains cumulative for the run;
GetTurnCounter reads usage since that turn began. Active reads include current provisional updates;
replay restores accepted counters and baselines without promising recovery of unsaved usage. No new
counter can decrease previously accepted totals when configuration changes.

Keep ExecutionContext.Iteration and event Iteration cumulative within their context. Read the
current turn's iteration count through GetTurnCounter(SCIterations). Turn limits use that turn
count; they do not reset operation identities or reinterpret earlier event iteration values.

All gauges are turn-scoped, including custom gauges. NewTurn resets their turn readings; rebuild
context-size gauges from retained state. A custom value that must persist across turns belongs in
LoopData or a cumulative counter. This explicit category rule avoids a second field-scope registry.
Gauge limits always inspect the current gauge value; Scope changes counter measurement only and does
not create historical gauge totals. Document that rule on Scope.

Keep current threshold operators: exact iteration limits are checked at completed iteration
boundaries with >=; other counter/gauge rules use >. Check already-exceeded saved usage before a new
dependent action, including after policy changes. A run limit does not reset at NewTurn. If a new
task is accepted when its run allowance is exhausted, it pauses without a model/tool action. Return
ExecutionPaused only after the recoverable stop save succeeds, preserving the same TurnId and
unfinished task for Continue under D111.

```go
limits := []gent.Limit{
    {
        Type: gent.LimitExactKey, Key: gent.SCIterations,
        MaxValue: 25, Scope: gent.LimitTurn,
    },
    {
        Type: gent.LimitExactKey, Key: gent.SCTotalTokens,
        MaxValue: 500000, Scope: gent.LimitRun,
    },
}
```

For cumulative usage 120 at the turn start and seven later increments, GetCounter returns 127 and
GetTurnCounter returns seven. Restore and Continue do not reset that usage; new work can increase
it. This separates accounting from policy without a second statistics engine. The tradeoff is a new
scope field and turn getter, plus the explicit change that every gauge now has a turn lifetime.

Do not add a persisted deadline API in this feature. The supplied Go context controls the runtime
owner/attempt lifetime and is replaced during Restore. Time already spent can be recorded, but a
fresh worker deadline is not a fresh counter allowance. A durable wall-clock deadline is a separate
future policy, not an inferred meaning of the current context.Context argument.

## Subscribers and Hooks

Recorder owns blocking persistence. Subscriber owns read-only observation and delivery-stop
notification. Hooks change effective inputs at the two approved stages. Keep these responsibilities
separate; a slow UI is not a persistence failure, and a failed save must block dependent execution.
Register Subscribers and Hooks on Config before creating/restoring contexts. There is no second
context-level UI subscription or committed-observer interface. (D51/D55/D58/D91/D142)

```go
// Subscriber receives read-only events and the end of delivery for its subscription.
type Subscriber interface {
    // OnEvent handles one read-only execution event.
    OnEvent(ctx context.Context, event *RecordedEvent) error

    // OnStop handles the end of delivery for this subscription.
    OnStop(ctx context.Context, result *SubscriberStop)
}

// SubscriberRegistration selects a subscriber and its delivery settings.
type SubscriberRegistration struct {
    Subscriber Subscriber
    Delivery   SubscriberDelivery
    Buffer     *DeliveryConfig
}
```

**Locked delivery modes D127.** Use these declarations:

```go
type SubscriberDelivery string

const (
    // SubscriberAccepted delivers events after their complete SaveBatch is accepted.
    SubscriberAccepted SubscriberDelivery = "accepted"

    // SubscriberLive delivers provisional events, subject to the output save barrier.
    SubscriberLive SubscriberDelivery = "live"
)
```

An empty mode selects SubscriberAccepted. Other unknown values are invalid configuration. Accepted
delivery includes all saved evidence, including reasoning, tool activity, and rejected answer
candidates; it does not mean accepted answers only. Delivery can arrive in bursts at savepoints.
Live delivers provisional progress before persistence. Gent must still withhold accepted final
output until its save succeeds under D113. Neither mode replaces historical replay.

Gent must document what each consumer must prepare. Accepted consumers handle batch bursts, replay,
and duplicate delivery. Live consumers also handle previews that disappear after recovery, attempt
changes, and replacement with accepted state. Both honor callback cancellation, treat values as
read-only, and use the identity fields in D130/D132. Put these requirements in the API comments, the
delivery/history guide, and focused Quick Start examples when implementing the API.

**Locked queue limits D128.** Keep this declaration and the current defaults:

```go
type DeliveryConfig struct {
    MaxEvents int
    MaxBytes  int
    Timeout   time.Duration
}
```

The defaults are 4,096 events, 8 MiB, and 30 seconds. A nil registration Buffer or a zero field
selects its default. Negative fields are invalid. Bounds apply separately to each registration in
each execution attempt. Queued events and the event inside an active OnEvent count toward both
allowances. Bytes measure encoded event size, not total Go heap use. Gent can calculate that size
once and reuse it across delivery accounting; no consumer-side encoding is required.

An event that cannot fit stops that subscription, including one event larger than MaxBytes. Do not
split it or silently drop it and continue delivery. Producers do not wait for a slow callback to
free capacity. Do not offer unbounded queues. Count and byte bounds cover different resource risks;
applications can raise them for larger events. These settings do not change D114's separately Locked
RecordingConfig or its persistence failure contract.

**Locked callback timeout and shutdown D129.** Use Timeout for the following three allowances:

| Stage | Allowance and behavior |
| --- | --- |
| Each OnEvent | One Timeout, starting when Gent invokes the callback |
| Normal final drain | One total Timeout after enqueueing ends |
| OnStop | A fresh Timeout context after event delivery has ended |

An OnEvent timeout covers the whole callback, including any network write inside it. Queue waiting
does not consume that individual callback timer. During the final drain, use the earlier of the
callback deadline and the drain deadline. Stop with SubscriberTimeout if the queue cannot drain in
time. A finite queue with only per-event timeouts could still delay shutdown for too long; the total
drain allowance prevents that delay without adding separate timeout fields.

On normal attempt completion, stop enqueueing, drain within that total allowance, then call OnStop
once. On overflow, callback timeout, or callback error, cancel that subscription and discard its
undelivered queue. Owner interruption or release also cancels delivery and discards the queue. Wait
for an active OnEvent to return before OnStop, and wait for OnStop to return. Both callbacks must
honor their contexts. A timer cannot forcibly terminate arbitrary Go code. Do not abandon a
goroutine and claim cleanup completed. No unconditional wall-clock bound is promised for a callback
that violates this contract.

A delivery failure does not undo accepted saves or change the agent's accepted result. Saved missed
events remain available for replay. Reuse one Timeout for these stages; no separate CallbackTimeout,
DrainTimeout, or StopTimeout field is added. Apply D121/D122's attempt cleanup and Release
contracts. This approval does not select or rename a public execution stop method.

**Locked stop notification and deduplication D130.** Use these declarations:

```go
type SubscriberStopReason string

const (
    // SubscriberClosed means the attempt ended or its owner ended delivery.
    SubscriberClosed SubscriberStopReason = "closed"

    // SubscriberOverflow means an event exceeded the queue's count or byte allowance.
    SubscriberOverflow SubscriberStopReason = "overflow"

    // SubscriberTimeout means callback delivery or the final queue drain timed out.
    SubscriberTimeout SubscriberStopReason = "timeout"

    // SubscriberFailed means an unexpected OnEvent error stopped delivery.
    SubscriberFailed SubscriberStopReason = "failed"
)

type SubscriberStop struct {
    RunId                           string
    AttemptId                       string
    Reason                          SubscriberStopReason
    LastDeliveredEventSequenceNumber uint64
    LastAcceptedEventSequenceNumber  uint64
    Error                           error
}
```

RunId and AttemptId identify the execution whose subscription stopped.
LastDeliveredEventSequenceNumber advances only when OnEvent returns nil.
LastAcceptedEventSequenceNumber reports Gent's accepted run position at stop. It is not a consumer
delivery cursor. Neither field proves that a browser received or displayed an event; application
acknowledgment is a separate contract.

Reason controls caller behavior. SubscriberClosed does not say whether the agent succeeded, paused,
or failed. Error carries an unexpected callback failure, not an expected timeout/overflow encoded as
an error. If an unexpected callback failure occurs during another active stop condition, preserve
that error without replacing the primary reason. A callback returning the cancellation caused by
Gent is not a separate unexpected failure. SubscriberStop is a runtime notification, not another
saved agent event. Keep OnStop's already approved void return; do not add a second stop channel.

Gent MUST document that consumers deduplicate events. Use these exact RecordedEvent fields:

| Delivery | Deduplication key |
| --- | --- |
| Live preview | RunId + AttemptId + EventSequenceNumber |
| Accepted event | RunId + EventSequenceNumber |

An OnEvent callback can send an event and then fail before returning nil. Its successful-delivery
cursor does not advance, so replay can deliver the event again. Gent does not promise exactly-once
external delivery. A replacement attempt can reuse unsaved sequence positions, so a Live consumer
must not treat EventSequenceNumber alone as identity. Cross-mode reconciliation must recognize that
a later Accepted event can confirm an earlier Live preview; confirmation is not another item to
append blindly.

For example, LastDeliveredEventSequenceNumber = 140 and LastAcceptedEventSequenceNumber = 150 means
that Gent accepted through 150, while this subscriber completed callbacks only through 140. An
Accepted consumer can fetch the saved events after 140. It must not treat 150 as proof of delivery.
Document this example, a callback-failure duplicate, and an interrupted Live attempt in the guide
and Quick Start. Put the keys and cursor meanings in Go documentation on Subscriber.OnEvent,
SubscriberStop, and RecordedEvent. D132 governs reconnect and preview reconciliation.

**Locked read-only event envelope D131.** Use the following public declaration. Its Go documentation
must preserve the mandatory read-only contract shown here:

```go
// RecordedEvent carries ordered execution evidence with a concrete, frozen payload.
// Consumers MUST NOT modify this envelope or any value reachable through its fields.
// Read fields only, including nested payload pointers, slices, maps, and byte buffers.
// Deduplicate Live previews by RunId, AttemptId, and EventSequenceNumber.
// Deduplicate Accepted events by RunId and EventSequenceNumber.
type RecordedEvent struct {
    EventSequenceNumber uint64
    RunId               string
    TurnId              string
    Ts                  time.Time
    Type                string
    EventName           string
    ContextId           string
    Iteration           int
    AttemptId           string
    ModelCallId         string
    ToolCallId          string
    Payload             any
}
```

EventSequenceNumber defines order; Ts is for display and diagnostics. Run/turn/attempt identities
associate activity with execution. ModelCallId and ToolCallId link related operations. ContextId and
Iteration identify where activity occurred. Type identifies the record kind and remains open to
application event kinds; EventName preserves the specific built-in or application event name. This
does not restore D116's removed parent/depth fields or child execution feature.

Payload holds a frozen concrete pointer, such as *StreamChunk, *BeforeToolCallEvent, or
*HistoryChange. Structural markers can have nil payloads. Remove the parallel Change field and
replace the public encoded Payload with the decoded value. CodecRegistry owns wire encoding and
restores concrete values on decode. Gent validates known kind/payload combinations before
publication. Subscribers do not need a codec argument or knowledge of the private wire format.

Consumers borrow all these values read-only, including when they retain references. Do not clone the
whole payload for each subscriber. Go does not enforce this ownership rule, so documentation must
state it directly. A consumer that needs a mutable representation must create independent values for
that feature; this does not permit mutation of the shared event. Apply the same warning to
Subscriber.OnEvent, envelope field documentation, guides, and examples. Hooks remain the separate
approved way to change effective execution input at their supported stages.

**Locked ordering and UI reconciliation D132.** Use one serial delivery worker per registration per
attempt. Its callbacks do not overlap. Different registrations and different runs can execute
concurrently, including registrations that share a Subscriber instance. Gent must not hold execution
locks while calling user callbacks. Failure stops only that registration in that attempt. Preserve
D91's once-only OnStop and ordering relative to an active OnEvent.

A UI can use the existing registration shape to receive both modes:

```go
Subscribers: []*gent.SubscriberRegistration{
    {
        Subscriber: liveUI,
        Delivery:   gent.SubscriberLive,
    },
    {
        Subscriber: savedUI,
        Delivery:   gent.SubscriberAccepted,
    },
}
```

This is a Config literal fragment. liveUI and savedUI are application handlers forwarding to one
application hub, not new Gent helpers. Separate handlers identify the delivery mode without adding
it to each saved event. The two registrations have separate bounded queues. Browser connection
management belongs to the application; no combined delivery mode or new subscription method is
added.

Subscriptions deliver future events. On reconnect, load saved history to a fixed accepted head,
buffer new delivery during that load, then reconcile by the D130 identities. Accepted history is the
durable basis; an unsaved Live preview must not become a trusted replay cursor. A UI uses existing
source-message identities to relate candidate previews to complete accepted output. Do not append a
candidate preview and its accepted publication as two answers. Keep D113's candidate/accepted
distinction and original-versus-processed content evidence.

**Subscriber documentation and verification requirements.** Implementation must update API comments,
delivery/history documentation, and focused Quick Start examples together. Include both modes,
read-only payload access, cooperative cancellation, queue sizing, duplicate detection by the exact
fields, failed-callback duplicates, stop cursors, saved-history reconnect, and shared-instance
concurrency. D81's later chat example must demonstrate this public contract.

Extend D63 verification with count/byte overflow including the active callback, one oversized event,
nil/zero/negative configuration, callback deadline coverage, and a total final-drain deadline that
does not reset per event. Use controlled synchronization to prove serial callbacks, concurrent
independent registrations/runs, active-callback completion before OnStop, and worker cleanup. Cover
failed-callback duplicates, reused unsaved sequence positions, both deduplication keys,
accepted-state preservation after a delivery failure, and immutable typed payload replay. Preserve
unexpected shutdown errors without replacing the primary stop reason. These are implementation
checks, not claims that the unimplemented interfaces have already passed runtime tests.

**Locked complete initial Hook set D142, 2026-11-17.** The owner approved the two-callback proposal
and asked to lock it in. The following declaration is the complete Hooks type for this refactor. D82
already approved the fields; this decision closes the initial set and its scope.

```go
type Hooks struct {
    BeforeModelCall func(ctx context.Context, event *BeforeModelCallEvent) error
    BeforeToolCall  func(ctx context.Context, event *BeforeToolCallEvent) error
}
```

Both callback fields are optional. A nil callback means no Hook at that stage. Config.Hooks remains
*Hooks under D114; nil means no Hooks. Keep one callback per stage. Applications compose several
transformations inside that callback in an explicit order and return the first failure. Do not add
an ordered Hook registry, a generic middleware API, or a mutation callback for each observed event.

Use the existing extension contracts for their separate responsibilities:

| Requirement | Extension |
| --- | --- |
| Add temporary model-request context | BeforeModelCall |
| Normalize effective tool arguments | BeforeToolCall |
| Display progress or record metrics and results | Subscriber |
| Validate a proposed final answer | OutputCustomValidator |
| Process or transform a proposed answer | Output.Process |
| Change iteration logic or persistent custom state | Loop.Next |
| Reset custom turn state | LoopData.ResetTurn |

Keep the following execution rules:

- Run the Hook synchronously before the dependent operation and before freezing its effective input.
  Validate the effective input after the Hook. Capture evidence under the existing D54/D61 savepoint
  schedule; the Hook does not add a model-response savepoint or weaken a save barrier.
- Limit Hook mutation to the effective model request or tool arguments. Do not change operation
  identities, the accepted task/history, or earlier saved evidence. After return, the callback must
  not retain mutable access and change input that Gent now owns.
- Apply D103 to a Hook error: prevent the dependent action, return the error to the application, and
  preserve accepted state for restoration. Do not convert that error into ordinary model steering or
  accept a terminal error result. Expected tool denial belongs in the tool's explicit response, such
  as ToolBlocked. Keep the handled Tool.Call/Recover error contract separate.
- Preserve D73's repeat-safety rule. A Hook can run again after interruption of unfinished work; it
  has no exactly-once guarantee. Shared callbacks must support concurrent runs. Keep mutable state
  in its proper scope. Use managed tools for external actions that need managed recovery.

For example, BeforeModelCall can add current account information to the effective request while
keeping the saved Task unchanged. BeforeToolCall can normalize a path before the tool receives it.
Record the resulting effective inputs so accepted history shows what the operations received.

The tradeoff is a smaller, clear mutation API with less lifecycle customization through callbacks.
Persistent changes currently made from lifecycle subscribers must move into Loop.Next or a loop
wrapper and checkpointed LoopData. The existing integration-test PolicySuggestionHook inserts
persistent history from OnBeforeIteration; move that step and its completion flag into the loop's
saved state during implementation. Request-only suggestions can use BeforeModelCall instead. Do not
keep a mutable lifecycle Subscriber as a second way to change execution state.

Additional before/after lifecycle, response, compaction, and output Hooks are outside this initial
set. Observation uses Subscriber; behavior stays with the existing Loop, Tool, and Output contracts.
A later concrete use case can justify another mutation stage, with its ownership, validation,
interruption, and replay contract reviewed together. This decision does not forbid future growth.

Update API documentation, migration examples, and Quick Start material to explain these roles,
optional fields, explicit composition, read-only observation, repeat safety, and D103 failures.
Extend D63 with omitted callbacks, effective-input capture and validation, no dependent action on
Hook failure, preserved accepted state, and repeated callbacks after interruption. Verify both
replay paths without introducing a new savepoint or rerunning accepted external effects.

## Assistant content, model responses, and accepted publication

Use one per-logical-item AssistantContentType across streams, complete responses, history, and
requests. Preserve the previous assistant answer as history; NewTurn does not rewrite its phase or
turn processed output into a duplicate model answer. Use the same Content payload for previews and
accepted publication. Event kind and content classification determine their meaning.
(D18/D97/D99/D101/D104/D113/D135-D141)

Native provider final_answer maps to Gent final_answer_candidate, not accepted final_answer.
Adapters preserve native phases and continuation and translate Gent classifications for their
endpoint; do not forward Gent labels blindly. Only the managed acceptance path can publish
final_answer, through accepted_answer after validation and successful saving. A provider label,
complete candidate, validation success, or saved candidate evidence cannot establish acceptance.

| Stage | Gent classification and event |
| --- | --- |
| Unknown preview purpose | intermediary or unspecified provider progress |
| Answer candidate | final_answer_candidate; model_stream_chunk or complete response evidence |
| Rejected answer | Remains a candidate; retain validation and correction evidence |
| Validated; save pending | Still no accepted-answer publication |
| Terminal save accepted | Complete final_answer in accepted_answer |

Preserve earlier frozen preview labels when the classification becomes known later. Reuse item
identity, and save the complete response with its known classification. Never rewrite original
candidate events into accepted answers. Publish processed accepted content as separate linked
evidence, even if it differs from the original. Candidate previews and complete accepted output must
not be appended blindly as two answers in a UI. (D99/D104/D113)

**Locked assistant-content group D135.** Use this exact declaration and pointer receiver:

```go
type AssistantContent struct {
    Type      AssistantContentType
    MessageId string
    Content   []ContentPart
}

func (*AssistantContent) isContentPart() {}
```

One item represents one logical assistant item, such as reasoning, commentary, or an answer. Its
Content retains ordered text and media. Reuse this ContentPart representation in responses, history,
requests, and streaming. Do not add a message-wide classification to a mixed response. Allow
text/media inside AssistantContent; keep tool-call parts outside it. Reject nested AssistantContent
groups and embedded tool calls at the managed boundary under D103. Existing content readers and
codecs must understand the wrapper. This avoids another public part interface.

**Locked ordered response D136.** Use the following complete ContentChoice declaration:

```go
type ContentChoice struct {
    Parts           []ContentPart
    Continuation    *ProviderContinuation
    CandidateSource *ContentSource
    Completion      ModelCompletion
}
```

Parts is the ordered readable response. It contains assistant groups and native tool-call request
parts in their original order. Remove the separately stored CandidateContent, MetaContent, and
ToolCalls slices. They must not remain as parallel mutable copies or compatibility aliases.
CandidateSource retains its existing selection/provenance role. Completion describes the provider
response outcome; it does not establish Gent's accepted output.

Keep Continuation because readable parts cannot represent all required native provider state.
Adapters must preserve usable native items and must not send both those items and duplicate readable
copies on the next model call. D104 still preserves original response and processed output as
related archive evidence rather than two assistant answers. The tradeoff is changes to every adapter
and content codec in return for one ordered readable representation.

**Locked derived response views D137.** Use these methods instead of the removed stored slices:

```go
func (choice *ContentChoice) CandidateContent() []ContentPart
func (choice *ContentChoice) ToolCalls() []*ModelToolCallRequest
```

CandidateSource.MessageIds explicitly selects the assistant items for output processing and links
them to earlier previews. CandidateContent returns their inner text/media parts in response order. A
missing selection returns no candidate. ToolCalls returns the recorded tool-call parts in response
order. Existing completion and argument checks still determine whether a recorded call can execute.
Do not treat the presence of a call or a candidate label as permission to execute or publish it.

Validate source identities and candidate classifications before processing. An invalid adapter
response follows D103; do not silently select commentary or reasoning as replacement output.
Classification describes purpose; source selection identifies which items the operation uses. The
methods return read-only views. A scan can allocate a slice of references without copying text or
media bytes. Callers can retain that view locally when needed more than once. Do not maintain a
second mutable response or add a cache that requires invalidation when public fields change.

**Locked stream content payload D138.** Use this complete content-payload declaration:

```go
type StreamChunk struct {
    Content          []ContentPart
    SourceMessageIds []string
}
```

Assistant output appears as *AssistantContent entries in Content. The logical item owns MessageId;
remove the separate chunk-level MessageId and MetaContent fields. SourceMessageIds links complete
accepted output to its source previews. Keep the D113 distinction between provider progress and
complete accepted publication. A UI accumulates progress by item identity and uses the publication
and its source links to handle the complete accepted output without duplicating the answer.

Move execution metadata out of StreamChunk. Use the already Locked D131 RecordedEvent for run, turn,
attempt, sequence, timestamp, context/iteration, and call identity. Keep stream grouping in the
associated call/source evidence. This approval does not add fields to D131 or restore the removed
child topology. Payloads and their event envelopes must retain their full association through
encoding, saved replay, live delivery, and UI projection.

Remove the chunk-level error channel. Obtain provider-operation errors through Stream.Response() and
the existing operation error paths/evidence. Preserve D103 propagation and diagnostics without
adding a second error result interface or a new savepoint. A bare chunk is no longer a complete UI
event. UI consumers use Subscriber's RecordedEvent envelope; low-level Stream.Chunks remains
provider progress. Update affected stream helpers and consumers in the same implementation.

D127-D132 supply the event and delivery contract. No extra streaming or acceptance enum is required.
D141 names the complete accepted-publication event accepted_answer. model_stream_chunk remains the
provider progress event. Both use the same payload type but have different meanings.

**Locked assistant-content constants D139.** Keep the named string type and use these constants. The
last two declarations reaffirm D113; their meanings do not change.

```go
type AssistantContentType string

const (
    // AssistantContentUnspecified means the item has no known classification.
    AssistantContentUnspecified AssistantContentType = ""

    // AssistantContentCommentary identifies visible progress or commentary.
    AssistantContentCommentary AssistantContentType = "commentary"

    // AssistantContentReasoning identifies exposed reasoning or its visible summary.
    AssistantContentReasoning AssistantContentType = "reasoning"

    // AssistantContentIntermediary identifies ordinary intermediate assistant content.
    AssistantContentIntermediary AssistantContentType = "intermediary"

    // AssistantContentFinalAnswerCandidate identifies a proposed answer before acceptance.
    AssistantContentFinalAnswerCandidate AssistantContentType = "final_answer_candidate"

    // AssistantContentFinalAnswer identifies complete output accepted and saved by Gent.
    AssistantContentFinalAnswer AssistantContentType = "final_answer"
)
```

Retain unknown provider classifications through this string type and provide a fallback display.
Unknown labels do not establish answer acceptance. Native phases still require adapter translation;
only Gent's managed acceptance path can publish accepted final_answer content under D113. When a
classification becomes known late, preserve earlier streamed labels and record the complete response
with its known classification. Reuse the logical item identity; do not rewrite frozen or saved
previews. Do not infer classification from the content's wording.

**Locked content filtering and ownership D140.** Provide one filtering helper:

```go
func AssistantParts(
    parts []ContentPart,
    kind AssistantContentType,
) []ContentPart
```

This replaces the purpose of MetaParts. Compose it with ContentText for text; do not add a separate
AssistantText helper. Remove obsolete metadata-specific helpers and aliases when replacing their
uses. Update existing content walkers to understand AssistantContent. CandidateContent remains the
selected output view; extracting all text from a mixed response is not equivalent to that view.

```go
reasoning := gent.ContentText(
    gent.AssistantParts(choice.Parts, gent.AssistantContentReasoning),
)
answer := gent.ContentText(choice.CandidateContent())
```

Returned views are borrowed and read-only, including reachable pointers, slices, maps, and media
bytes. A consumer that needs independent mutable data makes the copy for that feature. Gent makes
stable copies where capture requires them, rather than cloning every getter result. Keep D131's
mandatory read-only event contract in the related API comments and examples.

Adapters reuse an item's local ID across its deltas and complete response. Gent scopes that identity
to the physical call; replay preserves the saved identity. Accepted output receives its own ID and
links to original previews through SourceMessageIds. Preserve both original and processed forms
under D104, including when output processing changes the visible answer.

**Accepted-answer event — D141.** Use accepted_answer for complete saved-answer publication. The
content classification is final_answer. Do not retain execution_content or accepted_output as target
event names or compatibility aliases.

The event publishes the complete answer that Gent accepted and saved. It can contain text, media, or
structured content. final_answer is the content classification; accepted_answer identifies its
publication event. Keep model_stream_chunk for provider progress, with the same D138 payload type.
No candidate, validation success alone, or failed/uncertain save can publish an accepted_answer.
Preserve D113's acceptance and save barrier, source links, and original-versus-processed evidence.

Rename the event kind, trace constant, related emitters/consumers, wire values, fixtures, tests, API
comments, guides, and Quick Start examples together when implementing this refactor. In trace,
replace EventTypeExecutionContent with this value of the existing EventType enum:

```go
// EventTypeAcceptedAnswer identifies the complete answer accepted and saved by Gent.
const EventTypeAcceptedAnswer EventType = "accepted_answer"
```

This rename does not add a publication stage or change Output processing. Preserve the
AssistantContentFinalAnswer value and the save-before-publication contract. Current pre-refactor
source names are only evidence for the rename; the target event name is accepted_answer.

The troubleshooting UI and RunView must show the readable original model response and the accepted
output with their source relationship. Preserve native evidence in its supported form; do not invent
hidden reasoning. Later requests use selected usable original native continuation, not processed
delivery substituted to match the UI. Normal compaction can change active selection; both forms
remain in the archive. Include a transformed-output example in the chat application. Exact visual
presentation is application work. (D98/D104)

## Compaction and retained user messages

Use a generic LLM summary with bounded retained user messages beside compacted loop context. Retain
originals in the archive. Provider-native compaction endpoints and opaque provider compaction output
are outside this feature; neither is a Gent RunStateSnapshot. (D13/D22-D24)

The standard compactor owns UserMessageRetentionConfig and the replaceable retention strategy.
Configure compaction.NewSummarization(summaryModel, textFormat), then supply it through shared
Config.CompactionStrategy with a CompactionTrigger. Do not add a second retention configuration or
mandatory retention pass on Runner/Loop. A custom compactor may use its own policy while preserving
required instruction, history, output-correction, and native-continuation groups. (D94-D96)

```go
type CompactionStrategy interface {
    Compact(execCtx *ExecutionContext, input *CompactionInput) (*HistoryUpdate, error)
}

type UserMessageRetentionStrategy interface {
    Retain(input *UserMessageRetentionInput) (*UserMessageRetentionResult, error)
}

type UserMessageRetentionEntry struct {
    EntryId      EntryId
    MessageIndex int
    Message      *ModelMessage
    Truncated    bool
}

type UserMessageRetentionInput struct {
    Messages              []*UserMessageRetentionEntry
    MaxBytes              int
    Marker                string
    EarlierContentOmitted bool
}

type UserMessageRetentionResult struct {
    Messages              []*UserMessageRetentionEntry
    RetainedBytes         int
    EarlierContentOmitted bool
}

type UserMessageRetentionConfig struct {
    MaxBytes         int
    TruncationMarker string
    Strategy         UserMessageRetentionStrategy
}
```

The standard implementation has this fluent option; it returns the configured strategy, not error:

```go
// compaction package.
func (s *SummarizationStrategy) WithUserMessageRetention(
    config *gent.UserMessageRetentionConfig,
) *SummarizationStrategy
```

These declarations combine D25/D96/D106/D109/D110/D120. Input is borrowed read-only. EntryId and
MessageIndex identify the immutable source. Message is the current retained projection, not a
request to reload the original archive. Preserve chronological order and previous Truncated state.
EarlierContentOmitted carries prior omission across repeated compaction. Reuse unchanged values;
create a new projection for shortened content without changing originals. RetainedBytes includes the
notice. Marker is the effective setting, not another configuration owner.

Build retention input from saved user-authored origin, not message role alone. A human-role message
can hold an output correction, tool-error guidance, injected framework instructions, or a summary.
Such generated content is not user input and must not consume the user-retention allowance. Keep
source identity and origin through acceptance, restoration, and repeated compaction without
rescanning the full archive. D110 removes user-message protection; it does not make generated
guidance eligible. Required output-correction and native groups still follow their own rules.
(D13/D26/D110)

Retain proposes retained data only. It must not write history, reset counters, call a model, or
mutate original messages or execution state. The standard compactor combines the proposal with the
summary and next selection; Gent validates and accepts them at one compaction savepoint. This keeps
a custom retention policy from changing execution before the combined change is accepted.
(D26/D120)

### Defaults, byte accounting, and boundary selection

| Configuration | Behavior |
| --- | --- |
| No option or MaxBytes == 0 | Combined 32 KiB allowance |
| MaxBytes > 0 | Explicit allowance |
| MaxBytes < 0 | Invalid configuration |
| Empty TruncationMarker | Default notice |
| Non-empty TruncationMarker | Complete replacement notice |
| Nil Strategy | Default boundary-truncation strategy |

Count retained user content across all eligible messages. Measure text bytes, not JSON escapes or
message-envelope size; valid UTF-8 cuts can leave unused allowance. Do not add a disable flag,
negative unlimited allowance, or provider tokenizer/counting request. A byte allowance is not a
model-token window, billing cap, decoded-media bound, or total process-memory bound. (D90/D92/D109)

| Content | Byte charge |
| --- | --- |
| Text | UTF-8 content bytes |
| Embedded binary | Raw payload length |
| URL or file reference | UTF-8 bytes of the retained reference |
| Structured DataContent | Gent's stable compact JSON bytes |
| Cache-control wrapper | Underlying content once |

Do not fetch remote content for accounting. Omit older eligible messages before newer ones. In the
boundary message, remove older parts first; shorten text by dropping its beginning and retaining its
newest suffix. Cut only at a complete UTF-8 code point. Keep non-text parts whole or omit them:
never slice binary, structured data, or a reference. Returned messages and surviving parts retain
original chronological order. Custom retention remains replaceable through the same interface.
(D22/D93/D102/D107)

Boundary truncation uses more of the allowance by keeping part of a message that would otherwise be
omitted. Whole-message retention avoids clipped instruction fragments but can leave much of the
allowance unused. The default accepts that quality tradeoff; a custom strategy can choose complete
messages. Neither choice guarantees that the remaining text keeps every instruction or has equal
recall quality across models. (D22/D102)

Use this default notice:

```text
[Earlier user content was omitted during compaction.]
```

Provide one notice when any earlier content was omitted, including earlier compactions. Count its
UTF-8 bytes within MaxBytes. Prefix the first retained user projection; if none remains, use one
generated context note. Preserve framework origin and exclude the notice from future user-message
selection so repeated compaction does not accumulate copies. It is not a new user task. Do not
shorten the notice itself; an allowance smaller than the configured notice is invalid configuration
before the summary call. (D108/D109)

### Timing, coverage, and failures

Retention runs only when the existing compaction trigger fires at the normal before-iteration point,
including the first iteration of a turn. NewTurn does not trim unconditionally. A user message may
be shortened or omitted before the first model response or before the task completes. No user
prompt, including the newest task or P1, is protected from the configured policy. Do not add
Required, consumption tracking, permanent-P1 preservation, or a protected-input overflow state.
Fewer retained messages or an empty user selection are valid. (D24/D110/D120)

Apply retention at the same boundary as compaction to avoid extra changes to the request prefix
between compactions. This keeps opportunities for prompt-cache reuse while normal turns append
messages. Compaction does not necessarily invalidate the whole cache: an unchanged leading prefix
can remain reusable under the provider's rules. This is a design reason for the schedule, not a
guarantee of cache reuse or lossless recall. (D24/D120)

The summarizer sees attributed covered active input before this retention pass shortens the user
copies. Preserve roles, covered user text, the effective system prefix as read-only context, and
required tool/output-correction groups. It need not read the whole archive. Keep the current summary
prompt initially; the broader prompt/budget redesign in [req-compaction.md](req-compaction.md)
remains separate. Accept the summary, retained projections/notice, trigger state, and selection
together at the compaction savepoint. (D120)

Removing user protection does not remove CompactionInput.Required, native protocol constraints,
managed recovery safeguards, or required output-correction context. Preserve system instructions
outside the compacted selection. Invalid configuration, strategy failures, invalid updates, and
unusable requests follow D103. Do not add CompactionResult, UserMessageRetentionStatus, CannotFit,
RetentionLimit, or a retention-specific termination reason for the removed protection case.
Exceeding the configured allowance is a defect, not a normal success or expected overflow result.
(D103/D110)

Save enough accepted evidence for RunView to show the original messages/identities, actual summary
request and covered context, response/summary, effective retention settings, retained/omitted
sources, shortened projections/notices, selection changes, and the next main request. Claim that
content was summarized only when actual input supports it. A summary is not a guarantee that every
instruction is preserved or that the model remembers it. Failed or unsaved attempts retain D103's
private diagnostic path and do not add savepoints. (D98/D110/D120)

## Child-context removal and future subagents

**Child removal — D116.** The current implementation rejects restoration of active child contexts.
Remove that incomplete feature from this refactor. Keep a complete single-run execution and recovery
engine as the base for a later subagent feature. Do not retain unused child APIs or partial support.

Remove the following during implementation:

- SpawnChild, CompleteChild, parent/child traversal, child registration, recursive child cleanup,
  and child-specific execution/recovery branches. Do not add a replacement child preparation API.
- Config.Children, child binding validation, service inheritance, and child factory resolution. Keep
  Config.NewLoopData for the run's own custom data. D114's other Config rules remain unchanged.
- Child-only saved topology, parent identities, depth fields, lifecycle events, and tree views.
  Retain RunId, TurnId, attempt, model-call, and tool-call identities. Keep ContextId only where it
  has a separate current purpose. Do not retain empty hierarchy fields for possible future use.
- Hierarchical stats propagation, duplicate $self: counters, and Self/IsSelf APIs that exist only to
  separate child usage. Use ordinary counter keys in limits and presets. Keep run/turn usage,
  gauges, saved counters, and D111's resumable-limit behavior.
- Child-only tests, helpers, comments, guides, and examples. Move general recovery, event, limit,
  and cleanup assertions from child fixtures to direct-run tests. Update AGENTS.md and Quick Start
  when implementing the removal. Do not add compatibility shims for this development-stage API.

Reject unsupported saved formats through the existing load/restore contracts. Do not discard saved
child state and report that a partial restore succeeded. Keep independent runs and supported
parallel managed operations. Removal must not introduce process-wide execution locks.

Keep RunState separate from worker resources. Preserve RunStateSnapshot capture, complete SaveBatch
replay, RunView, custom-data capture, stable operation identities, managed tool recovery, accepted
output publication, NewTurn, Continue, and GetLastTurnResult. These approved features form the base
for later work. Do not add a future registry, scheduler, or child-state interface in this refactor.

**Future subagent contract — Locked.** Gent must give an AgentLoop a managed way to create and run
subagent Runners. Every subagent must have the same accepted-state, archive, custom-data, output,
limit, and recovery guarantees as the root. The application must use exactly the same root save/load
process for loops with or without subagents. It must not keep a separate child-state list, run a
child replay loop, or decide which saved child needs to restart. Gent handles those steps.

The application supplies its ordinary Recorder, code, configuration, and custom LoopData contract.
Gent records subagent task admission, identities, dependencies, accepted results, and recovery
evidence. Restoring the root and calling Continue must restore and continue its saved subagent work
without duplicate task admission or repeated accepted results. After root completion, NewTurn uses
the same normal caller flow. Framework coordination must preserve completed subagent work and
recover unfinished external operations through the managed recovery contract.

Full batch replay and RunStateSnapshot plus accepted tail must produce the same accepted logical
state for the root and its subagents. RunView must retain their complete accepted archive. The
existing limits on recovery still apply: Gent does not serialize a Go stack, promise identical
future model output, or undo external effects. Use the same execution/recovery engine for roots and
subagents. Exact future interfaces and internal storage layout need a separate complete design.
Ordinary separate Runner calls alone do not establish this managed subagent contract.

**Verification.** For this refactor, verify removal of child-only APIs and state, isolation between
independent runs, and unchanged root recovery at every approved savepoint. Preserve model/tool
concurrency, cancellation, storage, archive, output, and cleanup tests. Run all required native,
CGO-disabled, and race suites after code changes.

Before subagent support returns, use a custom review loop whose subagents check correctness and
security for one fixed commit. Use the same application root save/load helper for this loop and a
single-loop control case. The application helper must contain no child discovery or recovery code.
Test both replay paths, sequential and parallel work, nested subagents, and the following cases:

- Interrupt at every approved savepoint, including task admission, managed operations, accepted
  subagent output, and root completion. Compare complete state, identities, counters, and archive.
- Complete one review and interrupt the other. Reuse the completed result; continue unfinished work
  without a new task, repeated model/tool work for accepted results, or repeated output processing.
- Interrupt a tool after its external effect but before its result save. Exercise Completed,
  NotStarted, and Unresolved recovery evidence. Reuse accepted recovery preparation after a restart.
- Restore unfinished and completed roots. Verify Continue, root NewTurn, and additional subagent
  turns through the future managed interface without caller-maintained recovery steps.
- Change usable code/configuration or limits, then restore from the root. Preserve accepted usage
  and data. Insufficient unchanged limits must still stop work. Code errors retain D103's contract.
- Prove actual parallel operation overlap and consistent capture with deterministic barriers.
  Exercise blocked saves, concurrent custom-data changes, uncertain storage, and cancellation.
- Verify accepted event order, output save barriers, statistics, subscriber identities, and full
  RunView evidence. Repeated recovery and release must not leak workers or repeat accepted effects.

These future tests are the acceptance gate for reintroducing subagents. They do not require this
refactor to implement the deferred feature. Do not weaken the root guarantees to add subagents.

## Caller examples and documentation

### Recorder example

RunRecorder, RunStore, and wantRunStateSnapshot are application code. RunStore.Save serializes and
atomically accepts the complete batch and optional state, with the expected cursor, duplicate
receipt, and writer ownership checks. A nil state keeps the earlier stored RunStateSnapshot.
RunStateSnapshot frequency belongs to wantRunStateSnapshot; every batch is still saved.

```go
type RunRecorder struct {
    Store        *RunStore
    WantRunStateSnapshot func(*gent.SaveBatch) bool
}

func (r *RunRecorder) Save(
    ctx context.Context, batch *gent.SaveBatch,
    captureRunStateSnapshot func() (*gent.RunState, error),
) error {
    var state *gent.RunState
    if r.WantRunStateSnapshot(batch) {
        var err error
        state, err = captureRunStateSnapshot()
        if err != nil {
            return err
        }
    }
    return r.Store.Save(ctx, batch, state)
}
```

### Load a saved state

The application supplies these simple fetch functions. head, after, and through are accepted event
cursors. fetchRunStateSnapshot selects a capture at or before head. fetchBatches returns complete
batches after the RunStateSnapshot cursor through head, in order. Each function returns an operation
error if its storage operation fails. A full application loader must expose missing data as an
explicit outcome.

```go
func fetchAcceptedHead(ctx context.Context, runId string) (uint64, error)
func fetchRunStateSnapshot(ctx context.Context, runId string, head uint64) ([]byte, error)
func fetchBatches(
    ctx context.Context, runId string, after, through uint64,
) ([]*gent.SaveBatch, error)
```

Select one storage head, fetch the RunStateSnapshot bytes, and decode the same RunState type:

```go
head, err := fetchAcceptedHead(ctx, runId)
if err != nil {
    return err
}
data, err := fetchRunStateSnapshot(ctx, runId, head)
if err != nil {
    return err
}
loaded, err := codecs.UnmarshalRunState(data)
if err != nil {
    return err
}
if loaded.Status != gent.RunStateReady {
    log.Printf("Load: %s: %s", loaded.Status, loaded.Detail)
    return nil
}
state := loaded.State
if state.RunId() != runId || state.LastAppliedEventSequenceNumber() > head {
    log.Print("RunStateSnapshot does not match the selected run and head")
    return nil
}
```

Fetch and apply only later batches. Stop on a replay outcome that requires caller action:

```go
batches, err := fetchBatches(ctx, runId, state.LastAppliedEventSequenceNumber(), head)
if err != nil {
    return err
}
for _, batch := range batches {
    applied, err := state.Apply(batch, codecs)
    if err != nil {
        return err
    }
    switch applied.Status {
    case gent.ApplyApplied, gent.ApplyDuplicate:
    default:
        log.Printf("Replay: %s: %s", applied.Status, applied.Detail)
        return nil
    }
}
if state.LastAppliedEventSequenceNumber() != head {
    log.Print("Saved batches do not reach the selected head")
    return nil
}
// state is ready for runner.Restore(ctx, state).
```

Each snippet is part of an application operation that returns error. Expected load/replay outcomes
are logged and stop that operation; they are not converted into error types. These are focused
examples, not a complete loadRunState implementation. The finished helper must carry expected
outcomes to its caller explicitly. The earlier caller examples assume that load succeeded.

If no RunStateSnapshot exists, start with an empty RunState for the known run and replay its whole
accepted log. A production loader can read tail batches in pages to bound memory. Keep head fixed
during the load; do not chase a writer that continues to append. Check current writer ownership
before resumed execution, and enforce it on every save. Loading state alone does not grant that
ownership.

### Execute and read results

Use short examples that show inputs, calls, and results. Keep unrelated storage/server setup outside
each example. Name application helpers explicitly rather than presenting them as Gent APIs. Complete
Go programs are optional references. Document shared setup, first/later NewTurn, live and accepted
observation, Recorder capture, full/tail replay, Restore/Continue, and Restore/NewTurn. Include
error handling and cleanup where they explain the interface. (D44/D60)

For fresh execution, inside an application operation that returns error:

```go
runner := executor.NewRunner(loop, config)
execCtx, err := runner.NewExecutionContext(ctx, "conversation")
if err != nil {
    return err
}
defer execCtx.Release()

value, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Review the parser.")))
if err != nil {
    return err
}
if result.Status == gent.ExecutionFinished && result.TerminationReason == gent.TerminationSuccess {
    fmt.Println(value)
}
```

For saved state already loaded to a verified accepted head:

```go
restored, err := runner.Restore(ctx, state)
if err != nil {
    return err
}
if restored.Status != gent.RestoreReady {
    log.Printf("Restore: %s: %s", restored.Status, restored.Detail)
    return nil
}
execCtx := restored.Context
defer execCtx.Release()

value, result, err := runner.Continue(execCtx)
if err != nil {
    return err
}
if result.Status == gent.ExecutionNoActiveTurn {
    value, result, err = runner.GetLastTurnResult(execCtx)
    if err != nil {
        return err
    }
}
if result != nil && result.Status == gent.ExecutionFinished &&
    result.TerminationReason == gent.TerminationSuccess {
    fmt.Println(value)
}
```

For explicit new input after restoration or a clean pause/completion, use NewTurn on the idle
context. Stop requests a pause; wait for the active NewTurn/Continue call to return before another
execution call. Configure Subscribers before creating/restoring the context. No separate attachment
step, user-input receipt, or public admission call is needed. HTTP request deduplication and remote
Stop routing belong to the application under D133/D143.

The custom-loop lesson must explain complete LoopData serialization, optional local restart state,
managed model/tools, history, approved savepoints, and recovery after a tool effect but before its
saved result. Explain application versus framework responsibilities and the repeat-safety limits of
Output.Process, validators, and Hooks. (D60/D72/D73/D76)

## Implementation verification

**Required implementation coverage, Locked D63.** Treat savepoint recovery as a library contract.
Tests must verify complete saved state and actual continuation behavior, not only successful decode
or a few selected fields. Use named, table-driven scenarios through the public Runner/Recorder APIs.
Use a recorder/store that serializes accepted batches and optional RunStateSnapshots. Restore with
fresh objects, codecs, and runtime bindings so shared pointers cannot make the tests pass
incorrectly.

Run the following matrix across default ReAct and representative custom loops. Custom fixtures must
include more than one model/tool step and extra fields beyond embedded BasicLoopData. Cover each
documented lifecycle outcome, and check Continue or NewTurn according to that outcome; the guarantee
does not mean rerunning an accepted terminal answer through Continue.

D72 narrows custom-loop assertions to restored accepted data, framework recovery, and entry into
Next with that data. Include a custom loop with no step enum. Repeated local work and different new
model responses are permitted; do not require exact custom-code continuation or identical generated
text. The reference loop must still preserve its own recovery state and managed operation
identities. Callback tests should distinguish repetition before terminal acceptance from reuse after
acceptance under D73. These are implementation coverage requirements, not evidence of runtime
verification.

| Required area | Assertions |
| --- | --- |
| Every approved savepoint | Exact accepted data, framework phase, applicable continuation |
| Batch-only replay | All custom state restored without ever requesting a RunStateSnapshot |
| RunStateSnapshot plus tail | Same state as full replay for each valid capture position and tail |
| Custom LoopData | Preserve type, nested values, collections, zero values, and added fields |
| Terminal restoration | Restore custom state and outcome; do not rerun output processing |
| NewTurn | Retain prior custom state; reset only explicitly declared turn fields |
| Many turns | Preserve RunId, prior input/answers, summaries, state, and absolute cursors |
| After Recovery | Reuse accepted reports/messages and prepared state after another interruption |
| Tools | Reuse saved results; recover uncertain calls without repeating original effects |
| Model interruption | Retry unsaved work as approved; preserve accepted usage and limits |
| Compaction | Restore summary, retained messages, selection, native groups, and custom data |
| Save failure/retry | Block dependent work; retry the same batch without duplicate state |
| RunStateSnapshot capture | Fixed batch/state; lazy capture; stable value during later work |
| Data validation | Explicit rejection of invalid or missing data; no empty-state fallback |
| Runtime ownership | Fresh resources; correct cancellation, subscribers, hooks, and release |
| Evolved configuration | Preserve facts/state; apply usable current tools, limits, and policy |
| Long histories | Load retained state plus tail without reading the full preceding archive |

At each savepoint, inject interruption before the save, while it is blocked, after storage commits
but before acknowledgment, and after acceptance before the next operation. Test both restoration
from the latest accepted head and exact retry after an uncertain save result. Include partial
RunStateSnapshot publication, a stale RunStateSnapshot, no RunStateSnapshot, a RunStateSnapshot at
the head, and paged tails. Verify wrong-run data, gaps, reordering, conflicting duplicates, corrupt
bytes, and missing codecs without partly changing accepted state or starting external work. Use the
approved explicit outcomes without silently dropping accepted data or adding a fallback API.

For terminal/custom-state tests, save nondefault values, reconstruct a fresh custom instance, and
check every saved field before NewTurn. Check the data observed by the first step of the new turn,
then interrupt that turn and restore again. Assert that no initializer replaces restored values.
Include declared turn resets and run-scoped values together. Reuse the same Runner for separate
contexts to verify that one conversation's data never changes another conversation's saved state.

For recovery tests, cover Completed, NotStarted, Unresolved, failed checks, missing handlers,
already accepted results with an older plan cursor, and nested/parallel managed calls. Repeat the
After Recovery interruption cycle and verify exact messages, native call IDs, selection, counters,
LoopData, and resume position. Accepted Unresolved reports must not trigger automatic rechecking.
Later newly interrupted operations must still be recovered. A saved completion marker alone is not
enough unless the corresponding state and messages also round-trip correctly.

Assert that Restore does not call a model, execute or recover a tool, reset turn fields, or process
accepted output again. Continue performs required recovery, then enters Next under D72. NewTurn
saves its new input once and uses restored history/data. Verify final output is delivered only after
its save succeeds. Check current limits before further work; saved counters must not reset or be
counted twice. Model usage lost after the last savepoint follows the documented D57 caveat.

Provider fixture coverage must include every supported adapter for interrupted continuation and
terminal Restore/NewTurn, with exact full request comparisons and preserved native continuation data
where required. Use deterministic model/tool fixtures for semantic comparisons; do not require an
unrelated live model to generate identical text. Supported parallel managed operations must satisfy
the same recovery guarantees; do not admit unrecoverable work.

Exercise capture/acceptance races, blocked saves, cancellation, slow subscribers, and resource
release under the race detector. Measure capture, serialization, replay time, allocations, and bytes
for long logs with fixed retained context, plus large active state. Run the required full native,
portable, and race suites after implementation changes. Live-provider release checks retain their
existing gate. This design edit does not run Go suites or claim that the future contract is tested.

**Locked provider continuation verification D100, 2026-11-16.** Preserve native answer phases on
rejected candidates while using Gent final_answer_candidate. Tests must prove rejection followed by
continuation works for each provider. Require per-provider NewTurn tests as a separate path. Extend
D63 with these requirements for every supported provider adapter and applicable endpoint family:

- Reject an answer, send the saved candidate and correction context, and obtain another response.
  Include repeated rejections before acceptance so history contains multiple answer candidates.
- Complete a turn, call NewTurn, and obtain a response using the retained earlier conversation.
- Repeat the applicable paths after restoration. An unfinished rejected-answer turn uses Continue; a
  completed turn uses NewTurn. Cover full batch replay and RunStateSnapshot plus accepted tail.
- Check complete native request construction, order, roles, item grouping, and required continuation
  data. Preserve known phases, signatures, tool identities, and unknown native fields where needed.
- Verify no rejected candidate is published as accepted_answer, accepted output waits for its save,
  and RunView retains each candidate, validation decision, correction, and accepted answer.

AssistantContentType is a Gent classification, not a universal provider wire field. Each adapter
must translate the retained context into a valid request; do not forward Gent classifications
identically to endpoints that use a different representation. Tests must prove the next call works,
not merely that a portable enum survived encoding. Do not silently change native phases or discard
evidence to make a test pass. An unsupported result does not establish support for the normal
same-provider rejection or NewTurn path; fix the mapping or report the limitation.

Use fixture tests for exact requests and saved-state behavior, plus configured live provider tests
before claiming remote compatibility. A controlled validator can force rejection without depending
on a model producing invalid output by chance. Missing credentials, unavailable endpoints, and
unverified models must be reported; skipped live checks are not proof of compatibility. This adds
implementation and release verification requirements, not permission to implement or run paid
provider calls during this design discussion. Native-context changes after instruction replacement
or compaction must retain valid requests under D87/D103/D119.

Also enforce every focused check in the topic contracts above: empty/mismatched replay identity;
failed/uncertain admission and pause saves; Stop races and stale commands; no inherited stop flag;
read-only results and payloads; live/accepted deduplication; exact effective Hook inputs; callback
repeat safety; queue accounting and total drain timeout; content nesting and candidate source
validation; mixed media and native order; accepted-answer source links; retained-user suffix cuts,
notices, and empty selections; explicit limit presets and gauge/baseline restoration.

Verify both accepted-state equivalence and complete archive reconstruction. Do not use a compact
RunStateSnapshot as proof that earlier archive evidence exists. Preserve no-hidden-default limits,
D103 errors, and normal provider continuation while removing old API paths. Add a repository-wide
search for retired names and update runtime, codecs, trace, fixtures, comments, guides, generated
files, and Quick Start together. Source tests must exercise real public flows and exact complete
values, following AGENTS.md. (D63/D66/D75/D98/D100)

**Locked final implementation step D75: apply the enum rule across the repository.** After the
approved feature work, review all Gent-owned code and bring every enum into compliance with D70.
Complete this step before final verification; adding the AGENTS.md rule alone is not enough.

- Find existing enums and enum-like constant groups, including untyped strings and integer/iota
  declarations. Review their meaning; do not restrict the search to names that contain Enum or to
  public declarations. Include internal packages, provider adapters, tests, test helpers, examples,
  auxiliary modules, tooling, generated code sources/templates, and documentation code examples.
- Give each enum its own named string type. Declare each allowed value as a typed constant. Put a
  documentation comment immediately above each value, starting with its name and explaining its
  meaning and use. Document valid empty/default values too. Use the language's equivalent type and
  declaration mechanism for enums in Gent-owned code outside Go; do not omit those files silently.
- Update affected callers, switches, conversions, codecs, schemas, fixtures, tests, and examples.
  Keep required provider/storage wire representations correct through explicit mapping when needed.
  Update generators rather than only generated output. Review serialized-state changes under the
  existing version/migration contract; do not silently change accepted saved evidence.
- Verify that the inventory has no remaining violations. Check declaration structure and per-value
  comments across the full scope. Add meaningful behavior checks where conversion affects encoding,
  replay, or provider mapping. Run the required native, CGO-disabled, and race suites, plus checks
  for affected auxiliary modules. Keep the existing release verification requirements.

This is required implementation work across all owned code, not a review limited to the new
multi-turn types. No code conversion or Go suite is performed by this Markdown-only approval.

Run the native, CGO-disabled, and race suites over ./... after implementation, with the required
300-second timeout. Before release or remote-compatibility claims, run configured live providers;
skipped checks and missing credentials are not proof. Investigate failures with evidence. Measure
retained-state loading, capture, encoding, allocations, and bytes separately from archive replay.
This requirements cleanup runs document checks only; it makes no new runtime verification claim.

## Follow-up chat example

**Follow-up examples after the multi-turn refactor: Locked scope D81.** Create the example directory
after the multi-turn refactor and its required verification are complete. Keep the examples in
repository source for development use and exclude them from Gent release payloads. They must run
with the local .env configuration and include a working streaming agentic chat UI. The purpose is to
teach normal public API use and verify that the interfaces support a real UI server. A static mockup
or a UI that reads private framework fields does not meet this requirement.

The server uses an in-memory key-value store of encoded bytes. It has no disk or database
persistence. Recorder.Save stores complete encoded SaveBatch values and optional encoded
RunStateSnapshots with their accepted ordering and exact-retry information. Recovery must decode
those bytes; keeping live ExecutionContext pointers as the saved representation would not
demonstrate the storage contract. The store remains available when an execution context is released
and restored in the same server. A server process restart clears it. Document that lifetime in the
example.

The chat UI must show the reasoning or reasoning summaries exposed by Gent and the selected
provider, tool-call progress and results, assistant output, and relevant execution progress. Keep
unavailable or opaque provider data in its supported representation; do not invent reasoning text.
Distinguish provisional Live updates from accepted facts and publish accepted final output only
after Save succeeds. A user can submit the next prompt after a completed answer in the same RunId.
Use the standard NewTurn, Restore, Continue, and GetLastTurnResult contracts with configured
Recorder, Subscriber, and Hooks. Keep live user steering outside this example's required scope,
consistent with D28.

Use Gent's encoding, decoding, replay, and typed event interfaces. The application supplies its
memory store, server transport, UI rendering, and ordinary tool/provider configuration. It must not
add a second persistence hook, parse Gent's private encoded format, or implement recovery inside the
UI. Browser reconnects and slow consumers must not invalidate accepted execution. Use the approved
Subscriber registration, stop, identity, and reconciliation contracts to support that flow.

Show both original model responses and processed accepted output with clear labels and source links,
including a transformed-answer example. Demonstrate Stop, same-task Continue, and NewTurn
replacement on idle state. Reject stale browser Stop commands through application routing. Keep
available reasoning distinct from opaque or unavailable provider data. (D104/D132/D143)

Exact frontend, transport, directory/module arrangement, commands, and release-exclusion mechanics
are implementation choices for the follow-up. A separate example module is an option, not a new core
requirement. Verify actual Gent release/module contents; .gitignore or exclusion from a normal build
does not prove release exclusion. Keep .env credentials on the server and out of committed examples
and browser code. Demonstrate decoded-byte recovery from the memory store; a process restart clears
it. (D81)

**Example verification — D81/D104/D132/D143.** Check the working application against these
requirements; exact fixtures and tooling are implementation choices:

| Scenario | Required evidence from the working example |
| --- | --- |
| First prompt | NewTurn starts turn 1 and streams available reasoning, tools, and answer |
| Later prompt | Same RunId and retained context; the next answer uses earlier conversation |
| Byte-store restore | Release the execution context, decode stored bytes, and restore it |
| Both replay paths | Batch-only and RunStateSnapshot-plus-tail reach the same accepted state |
| Interrupted turn | Continue follows D72/D61 without another prompt or repeated accepted effects |
| Completed turn | NoActiveTurn and GetLastTurnResult expose the accepted completed result |
| Reconnect/slow browser | Correct event order and accepted view; execution remains valid |
| Failed save | Dependent execution and accepted answer publication remain blocked |
| Process lifetime | Restart clears the memory store; the example does not claim disk recovery |
| Local provider setup | A configured .env provider runs the demonstrated flow |
| Release inspection | The example directory and its application assets/dependencies are excluded |

Use deterministic checks for replay and failure cases, then verify the browser flow with a
configured live provider. Check browser errors, streaming order, tool visibility, and repeated
turns. Follow the repository verification rules when implementing the application. The example
follows the completed refactor; it is not part of this documentation cleanup.

## Cleanup report

This consolidation applies the owner's latest-decision-wins rule through D143. It makes no new API
choice. The following conflicting declarations, stale requirements, and duplicate review sections
were removed or rewritten. Each replacement points to its governing approval.

| Removed or corrected requirement | Current governing decision |
| --- | --- |
| Per-turn runs and session/admission APIs | One run; NewTurn executes — D05/D67 |
| Snapshot API, saved-state wrappers, transfer helper | Recorder capture — D30/D40 |
| Start/Execute/Resume target paths and public restore bindings | Runner family — D31/D35/D39/D67 |
| NewTurn only after completion; ExecutionNeedsContinue | Idle unfinished replacement — D133 |
| CancelTurn/public Interrupt and terminal user Stop | Stop pauses with Stopped — D143 |
| Terminal limit stops and allowance resets on Continue | Saved limit pause — D111/D117 |
| Extra TurnStatus and old result Error/termination variants | One result — D103/D105/D123/D143 |
| Old LimitExceeded Key/Value fields | MatchedKey/CurrentValue — D124 |
| Reason diagnostics and incomplete replay status sets | Detail and full typed results — D112 |
| Empty-run-ID panic or automatic identity assignment | Validate at use — D126 |
| Release per answer; skip terminal custom data | Reuse and restore — D48/D121/D122 |
| Separate observers/UI subscriptions; optional OnStop | Subscriber — D55/D91/D127-D132 |
| Errors only with Failed; per-event timeout only | Preserve errors; total drain — D129/D130 |
| Encoded/parallel event payloads and private decoding in the UI | Read-only typed envelope — D131 |
| Extra mutation Hooks or mutable lifecycle Subscribers | Two optional before-call Hooks — D142 |
| AssistantMessageKind, message-wide kinds, MetaContent | Per-item content — D113/D135-D140 |
| Candidate final_answer; old publication names | Candidate split/accepted_answer — D113/D141 |
| Parallel slices; chunk metadata/error fields | Derived views/outer metadata — D136-D138 |
| Replacing original native history with processed delivery | Keep both linked forms — D104/D140 |
| Token budget, whole-message default, text-prefix cuts | Byte-budget suffixes — D90/D102 |
| Required prompts, permanent P1, consumption, CannotFit | Best-effort retention — D110 |
| Trim on each NewTurn; summarize full archive | Trigger and covered input — D24/D120 |
| Split retention config; partial config/result types | Standard compactor — D94/D109/D110 |
| Reprepend Task; append replacement instructions | Prefix plus selection — D85/D89 |
| Generic model-request rejection result/reset adapters | D103 errors; scope D119 |
| Automatic limits and all-stat resets | Explicit presets/turn baselines — D114/D117 |
| Child bindings, Config.Children, propagated/$self: counters | Remove child feature — D116 |
| Task stored in LoopData or task-aware factory | Context-owned Task/pure factory — D47/D83/D84 |
| Required step enum and custom-data migration API | Restart/evolve data — D72/D76 |
| Per-model save schedule and save-on-assignment helpers | Approved savepoints — D54/D61/D119 |
| Match Runner versions; defensive state/archive copies | Evolved config/ownership — D34/D78 |
| Old F/R/I draft registers and repeated readiness blockers | Current contract through D143 |

Retained: the complete recovery/archive guarantees, current interfaces, acceptance and ownership
rules, error diagnostics, required verification, performance constraints, follow-up chat scope, and
useful research/source evidence. Dated probe results remain historical evidence only. Removed
repeated verification diaries and obsolete implementation sketches do not remove any future test
gate. The original document remains in version history rather than a second competing spec.

Context review after cleanup restored five still-current constraints and reasons: local-only reset,
user-origin eligibility, proposal-only retention, compact-state identity/reference handling, and the
truncation/scheduling tradeoffs. Future cleanup must remove superseded choices while keeping valid
constraints, alternatives, and design reasons beside the current rule. These clarifications do not
change the approved interfaces or reopen decisions.

Cleanup verification checked all 36 current type declarations against their governing approval
sources and confirmed one declaration per type. The 37 current Go declaration/example blocks pass
syntax checks. The dated comparison and measurement sections remain unchanged. Checks also cover
retired declarations, stale review references, Markdown structure, and line lengths. No runtime
code, Go test suite, or provider call is part of this documentation change.

## Dated research evidence

The following appendix preserves the provider/harness comparison and its source references from
2026-11-09. It describes the inspected Gent version and external systems at that time. Old Gent
names and behavior in this appendix are observations, not target declarations or current blockers.
No web research or provider behavior was reverified during this cleanup. The normative contract is
above. Broader prompting ideas that were never approved are not implementation requirements.

**Scope and source record.** Seven public repositories were cloned with `git clone --depth 1` into
`/tmp/gent-harness-review.xDKiO0`. Five contain the relevant harness implementation. Cursor's public
repository contains its README and security information, not the harness. Claude Code's public
repository contains extensions, examples, release notes, and support automation, not its full
runtime. No unofficial Grok CLI, leaked code, or reverse-engineered package was used.

| Repository | Pinned commit | Evidence used |
| --- | --- | --- |
| `openai/codex` | `8703d32bd0ea5cf26768859a3dd476df906ba053` | Runtime and storage source |
| `xai-org/grok-build` | `2bdd1d6a6369de0e8c68132ea4539e9abd9e14a8` | Runtime and storage source |
| `anomalyco/opencode` | `fee1ca5aa9e1d62a7ca42ef7eeaa8c02fe6ed080` | Runtime and storage source |
| `NousResearch/hermes-agent` | `b553d17b2542c54bbdf28c7ce5a9be47f956c0cf` | Runtime and storage |
| `openclaw/openclaw` | `8012956a65c37fc4cda5f56d73017a32bb105ef7` | Runtime, storage, docs |
| `anthropics/claude-code` | `0ed94698aa0aeee471465fc9c867c2e8434c4d68` | Public files; docs |
| `cursor/cursor` | `654b1b4775ca67aef473bd31a14c8c04a1abde2d` | Public files; official docs |

Gent's inspected HEAD is `4492e28872d9846fec45d8eb676889d9545dae1b`. No production source was
changed for this comparison. These are pinned source checkouts, not a claim that every installed
release has the same behavior. Grok Build's public mirror was last committed on 2026-09-29; the
other four inspected revisions were committed on 2026-11-08 or 2026-11-09. The mirror can lag the
product. The [official Grok announcement][h-grok-announcement] links the repository used here.

The chronological review below also checked Gent HEAD
`05add22f0f7bfad54daadae9ded545f8a33c78eb`. The Task, ReAct, executor, checkpoint, and compaction
files used for this comparison have no changes between these two commits.

**Three different kinds of retention.** Keep these meanings separate when reviewing interfaces:

1. **Stored history:** original messages, tool evidence, and summaries remain available for reads.
2. **Active model context:** the selected messages and summaries sent on the next request.
3. **Saved execution state:** enough accepted state to resume control flow and apply later records.

A text summary reduces model context. A serialized RunStateSnapshot reduces restore work.
Neither operation implies the other. A saved summary alone does not contain Gent's tool recovery,
accepted-output state, counters, custom checkpoint values, or retry receipts.
Likewise, saving all of RunView as JSON does not make its size or validation independent of history.

**Original prompt retention in active context.** “Retained” in this table means selected for the
model, not merely stored. The archive can contain text that the next model call does not see.

| System | Earliest user prompt after repeated compaction |
| --- | --- |
| Current Gent | All frozen `Task.Messages` remain outside compaction |
| Codex CLI | Kept within a recent-message budget; no permanent first-prompt guarantee |
| Grok Build | Latest real request or active goal is restored; earlier asks enter summaries |
| Cursor | Older messages are summarized; exact first-prompt policy is not public |
| Claude Code | Early requests enter the summary; exact wording can be lost |
| OpenCode | Older asks enter summaries; recent exchange text has a separate allowance |
| Hermes | Early messages are protected initially; protection ends after first compaction |
| OpenClaw | Older asks enter summaries; recent context and active-request evidence are retained |

The important result is that one continuing conversation does **not** imply that all user prompts
remain verbatim forever. Gent currently protects the complete starting Task more strongly than
these ordinary chat histories. Whether Gent should keep that property for the root prompt remains
an interface question; the owner's desire to retain its meaning is already part of D05.

**Chronological prompt comparison, 2026-11-09 — current behavior, not proposed APIs.** Each row
is a harness. Each prompt column describes what the model receives at that event. Storage of the
original transcript is a separate property. This section adds detail to the retention table above;
it does not approve an interface or change D05.

**One scenario for all six tables.** Start with system instructions `S` and a user prompt `P1`.
During the first turn, admit user steering `S1`, then finish with answer `F1`. Submit user prompt
`P2` in the same conversation. During that turn, admit steering `S2`. Compact once before the next
main model call, while the second turn is still active. This is the **first compaction**.

Use ordinary, short text prompts. Their total size fits the smallest user-message retention
allowance discussed below. Assistant work and tool results make the history large. There are
enough messages before `S1` that it is outside Hermes's initial three-message protection. Keep
the same model, provider, settings, and conversation. Do not reset, fork, edit, or rewind it.
Use built-in compaction without custom hook overrides or extra client-authored context items.
Assume compaction succeeds. Do not assume that different harnesses choose the same history cut.

Steering means input that **has entered the active turn**, not an item still waiting for a later
turn. Select a steering mode where the harness offers separate steering and queue modes. `S1`
and `S2` can each contain several messages; retention rules apply to each message separately.
The examples use one message in each group to keep the lists readable.

`Full` below means the user's complete text remains in model context without summary. It does
not require an unchanged JSON envelope, a separate user-role message, or retransmission of cached
tokens. `Applies` means the current system instructions govern the request. Some providers carry
them in a separate request field. Neither term promises unchanged dynamic environment content.

Cursor and Claude Code rows describe documented behavior. Their exact internal request lists
are not public. A `?` marks an unknown policy or structure, not an observed omission.

**1. First loop.** The first main request starts the first turn.

| System | System prompt | First prompt |
| --- | --- | --- |
| Current Gent | Applies: frozen Task | Full: frozen Task |
| Codex CLI | Applies | Full |
| Grok Build | Applies | Full |
| Cursor | Applies; internal layout unknown | Full; documented chat behavior |
| Claude Code | Applies | Full; documented chat behavior |
| OpenCode | Applies | Full |
| Hermes | Applies | Full |
| OpenClaw | Applies | Full |

**2. First steering, without compaction.** Inspect the next main request after `S1` is admitted.
Assistant messages and tool results remain between the prompt groups; the columns do not show
those intervening records.

| System | System prompt | First prompt | First steering |
| --- | --- | --- | --- |
| Current Gent | Applies | Full: Task | Full only with caller append |
| Codex CLI | Applies | Full | Full: admitted user input |
| Grok Build | Applies | Full | Full text: interjection wrapper |
| Cursor | Applies | Full | Full text; attachment/layout varies |
| Claude Code | Applies | Full | Full: delivered after tools finish |
| OpenCode | Applies | Full | Full: inbox promoted at a step boundary |
| Hermes | Applies | Full | Full: separate user row |
| OpenClaw | Applies | Full | Full: delivered after tool results |

Gent has no built-in queue for human steering. A caller can append a user HistoryEntry through
`AppendRecord(entry, true)` at a point where it owns the context. That ordinary active entry
appears in the next ReAct request. This is an application action, not a durable input-admission
API or permission to mutate a running context concurrently. Gent's `SteeringMessages` setting
contains framework feedback templates; it is not a human-input queue.

The delivery rules differ. Codex queues input against the active turn. Grok's selected steering
mode drains interjections at safe points. OpenCode distinguishes active-step steering from
next-turn inbox input. Hermes appends a user row after tool results. OpenClaw can steer or defer,
depending on the runtime. Cursor's documented surfaces can attach immediate input to the latest
user message/tool results; they do not establish a uniform wire format. Claude Code can deliver
queued text in the same turn after current tools finish. Commands can have different timing.
Sources: [Codex input][h-codex-input], [Grok input][h-grok-input],
[Cursor input][h-cursor-input], [Claude input][h-claude-input],
[OpenCode inbox][h-opencode-inbox], [Hermes input][h-hermes-input], and
[OpenClaw queue][h-openclaw-queue].

**3. Second prompt after the first final answer, without compaction.** `P2` starts another turn
in the same conversation. `F1` and the preceding assistant/tool activity remain in active history
for the continuing harnesses, subject to their ordinary tool-output and provider projections.

| System | System prompt | First prompt | First steering | Second prompt |
| --- | --- | --- | --- | --- |
| Current Gent | Run ended | No new call | No new call | Cannot start next turn |
| Codex CLI | Applies | Full | Full | Full: new user turn |
| Grok Build | Applies | Full | Full | Full: new user turn |
| Cursor | Applies | Full | Full | Full: same chat |
| Claude Code | Applies | Full | Full | Full: same session |
| OpenCode | Applies | Full | Full | Full: same session |
| Hermes | Applies | Full | Full | Full: same session |
| OpenClaw | Applies | Full | Full | Full: same session |

Current Gent's accepted final answer ends the run. Terminal `Resume` or `Continue` returns the
saved result without another model call. A new Gent execution with a caller-built transcript
would be a different scenario. It would not prove that the existing run accepts a second prompt.

**4. Second steering, without compaction.** Inspect the next request after `S2` is admitted.

| System | System prompt | First prompt | First steering | Second prompt | Second steering |
| --- | --- | --- | --- | --- | --- |
| Current Gent | Run ended | No call | No call | No next turn | No next turn |
| Codex CLI | Applies | Full | Full | Full | Full |
| Grok Build | Applies | Full | Full | Full | Full: interjection |
| Cursor | Applies | Full | Full | Full | Full text; layout varies |
| Claude Code | Applies | Full | Full | Full | Full |
| OpenCode | Applies | Full | Full | Full | Full |
| Hermes | Applies | Full | Full | Full | Full |
| OpenClaw | Applies | Full | Full | Full | Full |

The first four tables describe prompt retention. They do not claim that every old tool output,
image, or reasoning block remains unchanged. Output clipping, file references, provider rules,
and separate pruning can apply even before a summary compaction.

**5. First compaction.** These cells show what happens to each prompt when compaction commits.

- `Kept`: the prompt text remains directly available after compaction.
- `Summary`: the prompt is source material for the summary, with no separate text copy promised.
- `Both`: source material for compaction and a separately retained prompt copy.
- `By cut`: kept if selected into retained context; otherwise depends on the summary.
- `Head`: protected early messages, outside the middle selected for summary.
- `Tail`: retained recent messages or their text projection.
- `Policy?`: the public documentation does not identify that prompt's exact selection.

Summary input can be bounded or sampled. `Summary` describes the selected path, not a guarantee
that every word reaches the summary model or survives its answer. `Both` does not promise that
the generated summary repeats the retained prompt. The next table shows the request structures.

| System | System prompt | First prompt | First steering | Second prompt | Second steering |
| --- | --- | --- | --- | --- | --- |
| Current Gent | Run ended | No call | No call | No next turn | No next turn |
| Codex CLI | Reused | Both | Both | Both | Both |
| Grok Build | Reused | Summary | Summary | Both | Summary |
| Cursor | Applies; layout? | Policy? | Policy? | Policy? | Policy? |
| Claude Code | Reused | Policy? | Policy? | Policy? | Policy? |
| OpenCode, text | Rebuilt | By cut | By cut | By cut | Tail |
| Hermes | Kept | Head | By cut | By cut | Tail |
| OpenClaw, text | Rebuilt | By cut | By cut | By cut | Tail/request |

The differences in this table have specific limits:

- **Codex:** all four short user messages fit in this scenario. Local text compaction retains
  recent user text within 20,000 tokens; remote V2 uses a separate 64,000-token retained-item
  allowance. A much larger set of user prompts can lose older text. Native output is an opaque
  compaction item, not necessarily a readable summary. Feature settings can retain other items.
- **Grok:** in the inspected human interjection path, `S1` and `S2` have the `Interjection`
  provenance. `is_real_user_turn` accepts `Human`, so the latest real query here is **P2**, not
  S2. `for_compaction()` removes the assistant/tool tail before reconstruction. The request to
  keep is restored separately; the interjections depend on the generated summary. This is a
  result of the inspected path, not a claim about every Grok input surface.
- **Cursor and Claude Code:** older conversation becomes summary context. The public sources
  do not expose the exact selected prefix, protected user-message list, or retained suffix for
  this scenario. Thus an unconditional `Kept` or `Summary` in an individual cell would overclaim.
- **OpenCode:** the text path selects a recent range at user boundaries and stores its text
  beside the summary. The default allowance is 15,000 estimated tokens. Its summary request can
  also include retained text when the whole request fits. Native compaction has a different rule:
  an endpoint can choose the replacement window, while a trigger path adds recent user messages.
- **Hermes:** with the default settings, the first compaction protects the leading system
  message and the first three non-system messages. That includes P1 in this scenario. S1 and P2
  depend on the tail cut. The latest actionable user input is protected; here that is S2. After
  the first compaction, the early-message protection ends. P1 can enter a later summary.
- **OpenClaw:** retained recent messages follow the summary boundary. It also records bounded
  evidence of the latest unresolved request. The cut can split a long turn and use a separate
  turn-prefix summary. Retained messages and bounded request evidence are different guarantees.

The system prompt is not replaced by a summary. That does **not** imply that it is absent from
the compaction request. Codex, Grok, and OpenCode can send current instructions while asking the
model to summarize conversation history. Gent and OpenClaw use separate summary instructions.
Instructions loaded as ordinary historical messages can have a different lifetime from `S`.

**6. After compaction — logical model requests.** These are message-list pseudocode, not literal
provider JSON. System fields, tools, and ordinary request metadata are omitted where not material.
The original system instructions are written as `S` even when the provider uses another field.

```text
H = [P1, ...work1, S1, ...work1b, F1, P2, ...work2, S2, ...work2b]
u(x) = user message containing x
C = summary instructions; Csys = the summarizer's system instructions
Summary = generated text; Opaque = provider-native compaction item
E = rebuilt environment, project, and runtime context
R = the recent range selected by that harness
M = the older or middle range selected by that harness
Head = Hermes's protected early non-system messages
text(x) = that harness's text projection, not a lossless native-message encoding
fit(x) = send x when it fits; use that harness's bounded fallback otherwise
sample(x) = bounded source text, with omissions possible
~ = documented concept only; exact role, ordering, and selection are not public
```

`H` also contains any ordinary context injections made by the harness. The named prompt groups
are shown so they can be tracked through the requests. This is the first compaction, so there is
no earlier summary. Later compactions generally fold an earlier summary into the new one.

| System | What is compacted | What is sent in the next main call |
| --- | --- | --- |
| Current Gent | Unreachable after F1 | No next call in this run |
| Codex, text | `fit([S,...H,u(C)])` | `[S,P1,S1,P2,E,S2,u(Summary)]` |
| Codex, native V2 | `compact([S,...H])` | `[S,P1,S1,P2,E,S2,Opaque]` |
| Grok Build | `fit([S,...H,u(C)])` | `[S,E,P2,u(Summary),reminders]` |
| Cursor | `~[C,older(H)]` | `~[S,Summary,recent?,historyRef]` |
| Claude Code | `~[C,history(H)]` | `~[S,reloaded,Summary,recent?]` |
| OpenCode, text | `fit([S,...H,u(C)])` | `[S,u(Summary + text(R))]` |
| OpenCode, native | `compact([S,...H])` | `[S,...replacementWindow]` |
| Hermes | `[u(C + sample(text(M)))]` | `merge([S,...Head,Summary,...R])` |
| OpenClaw, text | `[Csys,u(sample(text(M))+C)]` | `[S,summaryMessage,...R]` |

Read the pseudocode with these source constraints:

- **Codex:** the four user messages are retained because of the stated short-text assumption.
  Current prefix instructions remain first. Rebuilt context `E` is inserted before the last
  retained real user message, or before the summary when there is no such message. Pending input
  and extra retention features can change the shape. The local and native rows are alternatives.
- **Grok:** `E` includes the user/environment prefix and project instructions. The summary wrapper
  can include a transcript reference. No ordinary recent assistant/tool tail is copied back by
  the inspected `for_compaction()` path. There is no active goal in this scenario; a goal can
  replace P2 as the preserved request. Interjection text was visible before compaction even though
  its provenance excludes it from the latest-real-query selection.
- **Cursor:** `historyRef` is the documented history-file reference used to retrieve older detail.
  The placement, message roles, and existence of a retained recent suffix are not specified by the
  cited public description. The list is an explicit conceptual inference, not captured traffic.
- **Claude Code:** `reloaded` includes documented startup content such as root instructions and
  memory. Its placement and any recent suffix are not established by the public sources. The
  list is an explicit conceptual inference. The ordinary system instructions still apply.
- **OpenCode:** the text path combines the summary and serialized recent text in one user-role
  checkpoint message. It does not replay that recent range as the original list of assistant and
  tool messages. The recent projection omits reasoning and caps tool text. When the full summary
  request cannot fit, the compactor first removes the retained suffix from its input, then uses
  progressively smaller text forms. With native endpoint compaction, the provider chooses the
  replacement window. With a native trigger, it is `[...recentUsers,Opaque]` within the allowance.
- **Hermes:** `M` excludes the protected head and tail. The summary prompt can also carry derived
  focus and optional memory context. `merge` selects or merges the summary's message role to meet
  provider alternation rules. It can restate unfinished input after the summary. Head and tail
  tool results can be pruned even when their user prompt text is protected.
- **OpenClaw:** `M` is the range selected for summary; a split turn can require another summary
  call for its prefix. The summary message also carries bounded request and file-operation
  evidence. The system prompt is built outside the transcript summary. This row covers the
  inspected text compactor, not every optional native/provider runtime.

For comparison, Gent's **existing single-turn** summarizer can run before F1. Its default form is:

```text
Task = [S, P1]
G = selected active history, excluding pinned entries and retained recent entries
summary_call = [Csys, u(text(previous_summaries) + text(G))]
next_main_call = [S, P1, assistant(Summary), ...pinned, ...recent]
```

The built-in default retains zero recent entries unless configured otherwise. S1, if appended as
ordinary unpinned history, can enter `G`. Neither S nor P1 enters the summary request. Both remain
unchanged in the next main call. This is an existing behavior example; it does not continue the
ended run in stages 3 through 6.

**New facts exposed by the chronological comparison.** A prompt's role alone does not determine
its retention. Grok distinguishes human prompts from interjections. OpenCode can retain user
wording inside a serialized text checkpoint. Hermes treats its first and later compactions
differently. These facts are inputs to the Gent interface review, not locked retention decisions.

Sources for the compaction lists: [Codex text][h-codex-local] and
[native V2][h-codex-remote]; [Grok state][h-grok-state] and
[interjection provenance][h-grok-provenance]; [Cursor summaries][h-cursor-summary] and
[history retrieval][h-cursor-context]; [Claude context][h-claude-context];
[OpenCode compactor][h-opencode-summary] and [request projection][h-opencode-projection];
[Hermes compressor][h-hermes-summary]; [OpenClaw compactor][h-claw-core], including
`summarization-completion.ts:79`; and [Gent summarizer][h-gent-summary].

**Current Gent — verified baseline.** `Task.Messages` is the caller's complete starting prompt.
ExecutionContext freezes it. ReAct builds each request from that Task followed by active history.
This applies to every message in Task, including earlier user and assistant messages supplied by
the caller. It is not a rule that protects only system-role messages.

A fixed original Task adds a fixed minimum prompt size. It does not by itself make each later
request grow. Moving every later user turn into that immutable Task would create growth that
compaction cannot reduce. The stored event archive has a separate growth and replay cost.

The built-in summarizer receives selected HistoryEntry values. It separates pinned entries,
previous summaries, recent entries, and entries to summarize. It sends previous summaries plus
the selected older activity to its summary model. The default `WithKeepRecent` value is zero.
Pinned and retained recent entries remain outside that summary input. After compaction, active
history contains the new summary, pinned entries, and retained recent entries, in that order.

The original Task is **not** automatically included in the summarizer request. Its prompt asks
for task intent, but the summarizer can only infer that intent from the history text it receives.
The main model still receives the original Task afterward. This is a material difference from a
harness that summarizes its entire conversation prefix. A custom strategy can read `ctx.Task()`;
the built-in strategy does not do that.

Summary extraction includes ordinary text, tool names, arguments, and textual tool results. It
does not translate opaque provider continuation data or binary media into summary text. Originals,
earlier summaries, and selection changes remain in History. A later summary can lose a fact while
the original record remains readable. The existing repeated-compaction recovery test exercises
exactly that distinction.

An accepted ReAct answer is recorded outside the active selection, then the run terminates.
`Resume` of a terminal run returns its saved result; it does not admit another user message. The
current `Continue` resumes execution state, not a conversation after a final answer. Thus current
Gent has recoverable single-run work and persistent history, but no finished-answer-to-new-input
lifecycle. This finding does not assume any of the session proposal's APIs.

Sources: [Task][h-gent-task], [ReAct request assembly][h-gent-react],
[compaction input][h-gent-history], [built-in summarizer][h-gent-summary],
[repeated-compaction recovery test][h-gent-summary-test], and [restore][h-gent-restore].

**Codex CLI — source verified.** One thread contains successive turns and context windows. A turn
ending does not end the thread. There are distinct local text-summary and remote checkpoint paths;
they must not be described as one fixed algorithm.

The local compactor builds a handoff summary from current history. It also restores real user
messages from newest to oldest within a 20,000-token allowance. It excludes earlier synthetic
summary messages from that user-message list. Early user prompts can fall outside the allowance;
an overlong retained message can be shortened. The local path is not a promise to retain every
original prompt. The remote V2 path retains eligible user messages and other supported items
within a 64,000-token allowance, then inserts an opaque compaction item. That retained selection
also favors newer content. Some developer and tool-output retention depends on feature flags.

After compaction, the harness rebuilds its initial context and world-state context. Stable
instructions therefore have a different lifecycle from ordinary conversation messages. A summary
can include facts from a user request without retaining that request as a separate full message.
Remote checkpoint content is opaque; this review cannot inspect the server's summary policy.

The storage implementation is directly relevant to the owner's RunStateSnapshot request. For
paginated rollouts it scans backward to the newest usable compaction containing replacement history
and a window number. It returns that saved base plus newer records. Legacy histories use a
full-history path. Reconstruction requires sufficient resume metadata and handles rollback; an
incomplete newest checkpoint does not permit blindly selecting an older one. This is evidence for
bounded reconstruction of model context, not proof of an equivalent full Gent RunStateSnapshot.

Sources: [local compaction][h-codex-local], [remote V2][h-codex-remote], and
[thread-store source][h-codex-store], especially `local/model_context.rs:27`, plus
[resume reconstruction][h-codex-resume], line 65.

**Grok Build — source verified.** Its session state retains a conversation across user requests.
The inspected compaction path assembles a new context with the system message, user/environment
prefix, project instructions, latest real user query, a generated summary, and runtime reminders.
The reusable builder supports retained recent messages, but this caller first uses
`for_compaction()`, which clears that assistant/tool tail. An active goal can replace the latest
query as the request to preserve. Human steering delivered as an `Interjection` is excluded from
the latest-real-query selector; it depends on the summary unless another path retains it.

This is an important source-versus-prompt distinction: the summary prompt says the next assistant
will see the user's original query. The actual selection code uses the latest real query, or the
active goal. It does not establish permanent retention of the first prompt. The summary prompt
asks for user requests, decisions, tool work, errors, pending work, and previous-summary facts.
That is a desired model output, not a deterministic guarantee that all those facts survive.

Grok keeps model-facing `chat_history.jsonl` separate from its `updates.jsonl` transcript and can
write compaction segments. Compaction replaces the working chat history. Transcript hints let the
agent retrieve older details. Segment detail is configurable and rendered segments have a size
cap, so a rendered segment is not automatically a complete lossless archive. The code also checks
tool-call/result pairing after reconstruction and can remove orphaned results.

Sources: [chat-state source][h-grok-state], `src/compaction_utils.rs:639,920` and
`src/persistence.rs:18`; [session source][h-grok-session], `compaction.rs:1845` and
`compaction_segments.rs:39`; [shared compaction source][h-grok-compaction],
`src/code_compaction/templates/full_replace_summary_prompt.txt`; and
[segment renderer][h-grok-transcript].

**Cursor — documentation verified; core not inspected.** Cursor documents summaries of older
messages when the context fills. Its context-discovery article states that it gives the agent a
history-file reference after summarization, so the agent can search for missing details. It also
moves large tool outputs to files for selective reading. Thus “not in current context” need not
mean “lost from the conversation.”

There is no public code-level guarantee in the reviewed material that the first user prompt stays
verbatim, nor a published serialized runtime checkpoint contract comparable to Gent's. Cursor
states that its harness is tuned per model. We should not infer one exact policy for every model
from a UI label or an extracted prompt. Sources: [summary documentation][h-cursor-summary],
[context discovery][h-cursor-context], and [public repository][h-cursor-repo].

**Claude Code — documentation verified; core not inspected.** A session is a saved conversation
that can resume after the process exits. Conversations are compacted into summaries that retain
requests and key code details, but early instructions can be lost. There is no documented promise
that the original prompt remains verbatim. Local transcripts are stored separately from the
active context.

System instructions and output style still apply after compaction. Project-root instructions and
memory are loaded again. Some other content, such as hook output and rules loaded on demand,
can be summarized away. This is more precise than saying every kind of instruction is immune to
compaction. Its file-edit checkpoints are also a separate concept from Gent's execution recovery.
Sources: [session persistence][h-claude-sessions], [context behavior][h-claude-loop],
[what survives compaction][h-claude-context], and [public repository][h-claude-repo].

**OpenCode — source verified.** A session contains successive user and assistant messages. Local
compaction writes a completed summary record, with a reference to the start of retained recent
text. The current default recent allowance is 15,000 estimated tokens. Selection respects user
exchange boundaries. That allowance is not a promise to preserve the whole original native tail:
the recent-text projection omits reasoning blocks and caps tool-result text.

The summarizer updates a previous summary with later conversation. Its prompt explicitly gives
newer history precedence and removes resolved requests and stale work. It excludes repository
instructions and environment setup from the summary because those are supplied separately.
Early user requests are ordinary summary input; there is no separate permanent first-prompt
insertion in the inspected request path. A first-user-message helper is used for session titles,
which must not be confused with context retention.

History loading queries the latest usable completed compaction and its suffix, then resolves the
retained-text reference. Native compaction is a different path: an endpoint can select a complete
replacement window, while a trigger path adds recent user messages to an opaque checkpoint.
Native windows are bound to the provider and model that produced them. Incompatible native
boundaries can be skipped so the loader can use earlier readable history. The original transcript
therefore serves a different purpose from the optimized model context.

Sources: [compaction][h-opencode-summary], [history loading][h-opencode-history], and
[recent-text projection][h-opencode-recent].

**Hermes — source verified.** The default compactor protects a head and recent tail. It summarizes
the middle after cheap tool-output pruning. Its default initial head protection is three messages
in addition to a leading system message. After one compaction, that extra protection becomes
zero. Resume also recognizes a saved summary and applies the same rule. The original prompt then
becomes eligible for summarization; it is not pinned forever.

Summaries accumulate prior-summary facts with newer activity. The implementation also preserves
the current actionable request and repairs tool-pair boundaries. Its historical-summary framing
distinguishes old work from the latest user request. That is useful for avoiding stale work, but
Gent must preserve the owner's distinction between a follow-up and an explicit cancellation.

The default storage path now compacts in place under the same session ID. SQLite soft-archives
summarized rows and inserts the new active context in one transaction. Archived rows remain
searchable. Watermarks, exact covered row IDs, and writer checks prevent a slow summary from
discarding messages that arrive while it runs. An optional older rotation path also exists; it
would be inaccurate to describe rotation as the current default.

Sources: [compressor][h-hermes-summary], especially `_protect_head_size` at line 4571;
[compaction orchestration][h-hermes-flow], line 4028; [storage][h-hermes-store], line 908; and
[resume tests][h-hermes-tests], line 177.

**OpenClaw — source verified.** Current built-in context reconstruction uses the latest compaction
or reset boundary, its retained messages, and the following messages. Compaction stores a summary
and `firstKeptEntryId`. Earlier user messages can enter the summary. The current human request has
separate bounded retention logic, including cases where its original row is no longer selected.
Internal updates must not silently replace that request.

The built-in summarizer works on text projections of current context and the previous summary.
It does not reload the entire original conversation for each compaction. Large inputs are budgeted
and can omit material; tool results and media also have projection rules. Safeguard mode checks
summary quality but has explicit degraded fallbacks. Therefore, it is not a lossless guarantee.
Tool pairs and split-turn context receive explicit treatment.

Current storage includes SQLite transcript projections and bounded context reads. Compaction keeps
the session identity in the built-in path. The current reset policy defaults to no automatic reset;
daily and idle resets require configuration. A continuing conversation is therefore the normal
case, not only a special mode. The repository also contains a document proposing a
further bounded resident-history migration. That document is marked **proposed**, and explicitly
says appends or full-history reads can still enlarge memory. We must not report that future design
as current behavior. Provider-native compaction has a separate replay contract.

Sources: [core context reconstruction][h-claw-session], `session.ts:27`;
[core compaction][h-claw-core], `compaction.ts:47,389`;
[current compaction documentation][h-claw-docs]; [reset policy][h-claw-reset], line 27; and
[storage design status][h-claw-storage], `resident-history.md:10`.

**System prompts and reasoning.** The owner's system-prompt assumption is broadly correct for
stable instructions, with two qualifications. First, a summarizer can see instructions without
the next main request replacing those instructions with summary text. Second, dynamically loaded
rules, hook context, and instruction-like user messages can be ordinary compactable history.
Roles alone do not define a permanent-retention policy. Current Gent uses the Task/history
boundary, not the system/user distinction.

The same distinction applies to “thought process.” We can retain emitted reasoning content,
decision notes, tool evidence, and opaque continuation items that a provider returns. We cannot
serialize model state that the provider never exposes. A textual summary preserves useful
conclusions and working state with possible loss. An opaque checkpoint can preserve provider
context, but it is not a cross-provider text summary. The inspected OpenCode and Codex paths make
this distinction explicit. Current Gent's summarizer already leaves opaque continuation items
outside its text extraction.

**Behavior checks for this comparison.** A public-API Go probe used a capture model, performed
compaction, then executed ReAct. Both cases passed: a single starting prompt and a supplied
starting transcript. It compared complete summarizer and main-model message lists. The original
Task was absent from summary input and present unchanged in the next main request. No provider
call was made. The source is `gent_context_probe_test.go` in the clone directory.

Two additional probes executed selected functions extracted directly from the pinned sources.
They did not reimplement those functions. Hermes passed five head-protection cases, including a
restart with a saved summary. OpenCode passed five recent-exchange selection cases. OpenClaw
passed four context-boundary cases. These are isolated policy checks, not full harness tests or
proof of database, provider, and concurrency behavior. The Hermes probe first lacked a referenced
prompt constant; adding that source constant corrected the probe before the successful run.

Reproduction from `/home/ricky/Personal/gent`:

```bash
go test /tmp/gent-harness-review.xDKiO0/gent_context_probe_test.go -count=1 -timeout 300s
python3 /tmp/gent-harness-review.xDKiO0/probe_context.py
node /tmp/gent-harness-review.xDKiO0/probe_context.mjs
```

The Rust toolchain is absent in this environment. Codex and Grok runtime tests were inspected but
not executed. Cursor and Claude Code internal behavior could not be tested from their public
repositories. No live provider behavior or summary quality is claimed by these checks.

**Gent suite checks for the harness comparison, 2026-11-09.** Native and CGO-disabled full suites
passed with `-count=1 -timeout 300s`. The standard full race run failed only the five-second
assertion in `TestExecutionContext_EmitChunkRecordsWithoutListeners`; it reported no data race.
The unchanged test then passed three isolated race runs in 8.012 seconds total. The assertion
measures elapsed wall time for 1,000 recorded chunks. These results do not establish why that
wall time exceeded the threshold in the full run. Package contention remains a hypothesis.
No assertion, test scenario, or production code was changed by this review.

The next full race run used sequential package execution. Its root package passed, but several
provider-dependent packages failed setup because `go.sum` lacked entries for
`github.com/googleapis/enterprise-certificate-proxy`. A status check showed new `go.mod`, `go.sum`,
Gemini, Vertex AI, and provider-test edits that were absent at the start. This review left those
changes untouched. This retry cannot validate the new provider work. The remaining
baseline check uses a `git archive` copy of the reviewed Gent commit in
`/tmp/gent-harness-review.xDKiO0/gent-baseline`.
The command was `go test -race -p 1 ./... -count=1 -timeout 300s`; the full suite passed.
This verifies the reviewed revision with sequential package execution. It does not erase the
standard race run's timing failure or validate the concurrent provider changes. The probes,
document references, line lengths, and source revisions also passed their checks.

**Chronological review checks, 2026-11-09.** The Go probe now has four cases. The two added cases
check the complete request with and without caller-appended user history. Both also call terminal
`Continue` and verify the same accepted answer and execution result, with no second model call.
All four passed against a clean copy of Gent `05add22f0f7bfad54daadae9ded545f8a33c78eb`.
The Hermes, OpenCode, and OpenClaw source-function probes passed again: 5, 5, and 4 cases.
These 18 focused cases do not test full external harnesses or live provider behavior.

The initial full native run in the working directory failed in `common` and `models` when HTTP
fixtures tried to open local ports. Both failures reported `socket: operation not permitted`.
The rerun with local test-port access passed. For stable verification, all three final full suites
used a `git archive` copy of `05add22f0f7bfad54daadae9ded545f8a33c78eb` at
`/tmp/gent-harness-review.xDKiO0/gent-chronology`. All three passed:

```bash
go test ./... -count=1 -timeout 300s
CGO_ENABLED=0 go test ./... -count=1 -timeout 300s
go test -race -p 1 ./... -count=1 -timeout 300s
```

Other work advanced Gent HEAD to `c691992e0d36f851434940fd833de0de07075c86` during verification.
Its only change from the tested revision is in `toolchain/js_wrapper.go`; the Task, ReAct, executor,
checkpoint, and compaction files used here are unchanged. This review did not edit that change.
The suite result applies to the recorded test revision. The six table shapes, references, local
links, code fences, line lengths, and all seven external source revisions also passed their checks.
No production code or API was changed by this chronological review.

Review artifacts: [clone manifest][h-manifest], [Gent probe][h-probe-gent],
[Hermes probe][h-probe-hermes], and [OpenCode/OpenClaw probe][h-probe-ts]. These files are in the
temporary review directory; the permanent source revisions and findings are recorded above.

[h-grok-announcement]: https://x.ai/news/grok-build-open-source
[h-gent-task]: /home/ricky/Personal/gent/agent.go:310
[h-gent-react]: /home/ricky/Personal/gent/agents/react/native.go:11
[h-gent-history]: /home/ricky/Personal/gent/context_history.go:185
[h-gent-summary]: /home/ricky/Personal/gent/compaction/summarization.go:626
[h-gent-summary-test]: /home/ricky/Personal/gent/history_compaction_test.go:40
[h-gent-restore]: /home/ricky/Personal/gent/context_checkpoint.go:325
[h-codex-local]: https://github.com/openai/codex/blob/8703d32/codex-rs/core/src/compact.rs
[h-codex-remote]:
  https://github.com/openai/codex/blob/8703d32/codex-rs/core/src/compact_remote_v2.rs
[h-codex-store]: https://github.com/openai/codex/tree/8703d32/codex-rs/thread-store/src
[h-codex-resume]:
  https://github.com/openai/codex/blob/8703d32/codex-rs/core/src/session/rollout_reconstruction.rs
[h-codex-input]:
  https://github.com/openai/codex/blob/8703d32/codex-rs/core/src/session/turn_input.rs
[h-grok-input]:
  https://github.com/xai-org/grok-build/tree/2bdd1d6/crates/codegen/xai-grok-shell/src/session
[h-grok-provenance]:
  https://github.com/xai-org/grok-build/tree/2bdd1d6/crates/codegen/xai-grok-sampling-types/src
[h-grok-state]: https://github.com/xai-org/grok-build/tree/2bdd1d6/crates/codegen/xai-chat-state
[h-grok-session]:
  https://github.com/xai-org/grok-build/tree/2bdd1d6/crates/codegen/xai-grok-shell/src/session
[h-grok-compaction]:
  https://github.com/xai-org/grok-build/tree/2bdd1d6/crates/common/xai-grok-compaction
[h-grok-transcript]:
  https://github.com/xai-org/grok-build/tree/2bdd1d6/crates/codegen/xai-compaction-transcript
[h-cursor-summary]: https://docs.cursor.com/en/agent/chat/summarization
[h-cursor-context]: https://cursor.com/blog/dynamic-context-discovery
[h-cursor-repo]: https://github.com/cursor/cursor/tree/654b1b4
[h-cursor-input]: https://cursor.com/docs/agent/overview
[h-claude-sessions]: https://code.claude.com/docs/en/sessions
[h-claude-loop]: https://code.claude.com/docs/en/how-claude-code-works
[h-claude-context]: https://code.claude.com/docs/en/context-window
[h-claude-repo]: https://github.com/anthropics/claude-code/tree/0ed9469
[h-claude-input]: https://code.claude.com/docs/en/interactive-mode#queue-messages-while-claude-works
[h-opencode-summary]:
  https://github.com/anomalyco/opencode/blob/fee1ca5/packages/core/src/session/compaction.ts
[h-opencode-history]:
  https://github.com/anomalyco/opencode/blob/fee1ca5/packages/core/src/session/history.ts
[h-opencode-recent]:
  https://github.com/anomalyco/opencode/blob/fee1ca5/packages/core/src/session/recent-text.ts
[h-opencode-inbox]:
  https://github.com/anomalyco/opencode/blob/fee1ca5/packages/core/src/session/inbox.ts
[h-opencode-projection]:
https://github.com/anomalyco/opencode/blob/fee1ca/packages/core/src/session/runner/to-llm-message.ts
[h-hermes-input]:
  https://github.com/NousResearch/hermes-agent/blob/b553d17/agent/turn_iteration_prep.py
[h-hermes-summary]:
  https://github.com/NousResearch/hermes-agent/blob/b553d17/agent/context_compressor.py
[h-hermes-flow]:
  https://github.com/NousResearch/hermes-agent/blob/b553d17/agent/conversation_compression.py
[h-hermes-store]:
  https://github.com/NousResearch/hermes-agent/blob/b553d17/hermes_state_messages.py
[h-hermes-tests]:
  https://github.com/NousResearch/hermes-agent/tree/b553d17/tests/agent
[h-claw-session]:
  https://github.com/openclaw/openclaw/tree/8012956/packages/agent-core/src/harness/session
[h-claw-core]:
  https://github.com/openclaw/openclaw/tree/8012956/packages/agent-core/src/harness/compaction
[h-claw-docs]: https://github.com/openclaw/openclaw/blob/8012956/docs/concepts/compaction.md
[h-claw-reset]:
  https://github.com/openclaw/openclaw/blob/8012956/src/config/sessions/reset-policy.ts
[h-claw-storage]:
  https://github.com/openclaw/openclaw/tree/8012956/docs/reference/session-management-compaction
[h-openclaw-queue]:
  https://github.com/openclaw/openclaw/blob/8012956/docs/concepts/queue.md
[h-manifest]: /tmp/gent-harness-review.xDKiO0/source-manifest.json
[h-probe-gent]: /tmp/gent-harness-review.xDKiO0/gent_context_probe_test.go
[h-probe-hermes]: /tmp/gent-harness-review.xDKiO0/probe_context.py
[h-probe-ts]: /tmp/gent-harness-review.xDKiO0/probe_context.mjs

### Pre-refactor performance evidence

These measurements concern the old full RunView replay/decode path, not the new compact RunState
capture cost. They explain the need for retained state and bounded tail replay. Preserve that
scope when comparing implementation measurements.

**Verified facts: measured behavior.** A public-API probe tested 100, 300, and 1,000 batches.
Each batch accepted one short human history entry and a control checkpoint. All entries stayed
active. It serialized the view five batches before the end, then compared full replay with decode
plus the last five batches. It compared the entire resulting `RunView`, validated it, restored an
execution context, and compared that context's exported accepted view too. All comparisons passed.

| Batches | Saved-view bytes | Full replay | Decode + 5 batches | ValidateBundle | Restore context |
| --- | ---: | ---: | ---: | ---: | ---: |
| 100 | 2,529,175 | 271 ms | 277 ms | 130 ms | 123 ms |
| 300 | 15,245,270 | 2.87 s | 1.60 s | 719 ms | 725 ms |
| 1,000 | 139,131,599 | 77.38 s | 19.18 s | 8.33 s | 6.67 s |

These are single local measurements, not production latency estimates. Full replay starts with
decoded batches; the saved-view path includes decode. Neither path includes database or network I/O.
Validation and restore were measured separately; restore includes its own validation. The large
all-active selection is deliberate. It shows the cost of retained transitions and repeated copies.
The 1,000-batch replay allocated about 19.0 GB in total; the saved-view path allocated about 2.0 GB.
Those numbers are cumulative allocations, not peak live memory. Small runs need not benefit from a
full-view capture. A compact execution state addresses the remaining growth.

The original measurement probe used public APIs to generate complete batches, measured replay
and codec load at increasing batch counts, and reported serialized size and allocations. Its full
source remains in version history. Reproduce performance checks against the implemented APIs;
do not retain the obsolete probe as a target example or infer a new RunState benchmark from it.
