The trace package projects events into runs, iterations, model calls, tool calls, and output. It
does not replace SaveBatch storage.

## Attach a sequencer

Use the run ID that the application obtained from its context:

{{example:trace-attach}}

## Subscribe with bounds

{{example:trace-subscribe}}

Read a current display view through the sequencer. Close the subscription and sequencer when their
owners are done. Inspect delivery errors instead of silently losing positions.

## Restore a display

{{example:trace-replay}}

Set `trace.Config.Redactor` to a `trace.Redactor` implementation when the display must hide private
fields. Redact the display projection, not the recovery archive. Never mutate borrowed model, tool,
or event values while building the display. API names such as `trace.Snapshot` refer to display
state; they are not a RunStateSnapshot for execution.

## Hide private display fields

{{example:trace-redact}}
