`NewTurn` and `Continue` return an answer, an `ExecutionResult`, and an error. Check the error
first. Expected control conditions use `result.Status`; they are not Go errors.

## Read the result

Inside your result handler:

{{example:control-status}}

`TerminationReason` explains the completed outcome or pause. A completed refusal is different from a
successful answer. Do not display a prior turn's output as the result of a rejected call.

## Request a pause

Call this from the component that controls the active conversation:

{{example:control-stop}}

Stop is nonblocking and idempotent. It does not prove a save completed. The active execution call
owns the pause save and cleanup. Route remote Stop commands to the correct run and turn. Discard
stale commands before they can affect a later task.

## Cancel the worker

{{example:control-cancel}}

Use that `ctx` when creating the context. Here `runner` and `task` are already configured:

{{example:control-timeout-use}}

Call `cancel` when the owner is done. Go context cancellation interrupts execution. `Release`
disposes of the handle and joins workers; it does not complete an unfinished task or make a terminal
save. Do not call Release synchronously from a callback that it must join. A callback can use Stop.

## Fix, restore, and continue

An unexpected Hook, loop, output, or request failure returns zero T, nil result, and an error. No
terminal error save accepts the broken provisional state. Preserve the error for diagnostics, fix
the cause, [restore accepted state](/storage/restore/), then Continue.

Do not retry unchanged failing code. Do not resubmit an accepted task. A failed save can have an
uncertain outcome; reconcile storage before another writer starts. Tool errors and subscriber
failures have their own contracts on their subject pages.
