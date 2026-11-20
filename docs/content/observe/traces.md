The trace package projects events into runs, iterations, model calls, tool calls, and output. It
does not replace SaveBatch storage.

## Attach a sequencer

Register a sequencer before creating the execution context. An empty run ID binds it to the first
event from that conversation:

{{example:trace-attach}}

Create a context and run tasks with this Runner as in [Hello World](/start/hello-world/). Keep one
sequencer per conversation. It rejects events from a different run after it binds the first ID.

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

Apply these Redactor callbacks to the `trace.Config` passed to NewSequencer in the first example.
Register that sequencer as the Runner's Subscriber in the same way.
