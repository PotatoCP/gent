Managed execution saves effective tool input before the call. It accepts the result and plan
progress together afterward. If the result is uncertain after interruption, recovery inspects the
original operation.

## Use the operation identity

Inside a managed tool handler:

{{example:tool-identity}}

ToolCallId stays stable for that logical operation. AttemptId identifies the interrupted worker.
External services can use the logical ID for their own idempotency contract.

## Register inspection

`ledger.Inspect` is an application operation that returns evidence without repeating the action.

{{example:tool-recover}}

## Report a completed operation

Return this only when inspection proves the result:

{{example:recovery-completed}}

## Report uncertainty

{{example:recovery-unresolved}}

Use NotStarted only when evidence proves the original action did not start. NotStarted and
Unresolved must not include Execution. Completed requires it. A Recover error ends that inspection,
keeps raw diagnostics private, and supplies safe model guidance.

Gent reuses accepted call results and accepted recovery preparation. It does not call the original
operation again simply because recovery completed. The model decides later actions. Keep deprecated
tool bindings while unfinished saved calls need them. Current catalog changes do not permit
rewriting accepted historical replies.

## Report that work did not start

After the service confirms this outcome:

{{example:recovery-not-started}}

## Keep an old recovery binding

Use the current registry and the old tool implementation:

{{example:recovery-binding}}
