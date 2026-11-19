Register current codecs and bind current services before loading state. Gent permits changed code
and configuration when saved data remains usable. Restore does not require an identical Runner
version.

> Full batch replay and a RunStateSnapshot plus its accepted tail restore the same accepted logical
> state at the same position. Continue resumes that state. Future model output can differ.

## Replay all batches

`saved` contains complete encoded batches in accepted order. `runId` is the known non-empty run ID.

{{example:restore-batches}}

Apply checks the whole batch. Rejected or duplicate Apply leaves the inputs unchanged. A successful
Apply takes ownership of retained values. Do not apply those same mutable values to another target.

## Load a RunStateSnapshot

`data` is the encoded capture:

{{example:restore-state}}

Apply the `tail` returned by storage:

{{example:restore-tail}}

## Prepare the context

Use a Runner with the current model, tools, recorder, subscribers, Hooks, and limits:

{{example:restore-context}}

A Ready Restore transfers state ownership. Other outcomes leave the input with the caller. Restore
creates local resources. It does not run models, recover tools, or process output.

## Continue the task

{{example:restore-continue}}

After a completed turn, read GetLastTurnResult or call NewTurn with a new task. Continue does not
reset counters or grant a new allowance.

## Check expected outcomes

| Operation | Outcomes to handle |
| --- | --- |
| Decode RunState | Ready, Invalid, Incompatible |
| Apply | Applied, Duplicate, CursorConflict, ReceiptRequired, Invalid, Incompatible |
| Restore | Ready, Invalid, Incompatible |

Status controls the branch. Detail explains the condition. Unexpected operation failures use error.
An empty ID is allowed at target construction but rejected at Apply or Restore. Gent never invents
an ID to make replay fit. Do not skip a gap or assume a missing duplicate receipt proves acceptance.
