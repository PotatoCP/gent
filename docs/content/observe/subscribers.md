Subscribers receive future events. They do not replace persistence or archive replay. Configure
registrations before creating or restoring the context.

## Read events

This small log is for a short example. A server should forward into its own bounded transport.

{{example:subscriber-events}}

## Handle the end of delivery

{{example:subscriber-stop}}

OnStop runs once for a registration's attempt. The callbacks are serial within that registration.
Different registrations and runs can overlap, including handlers that share the same instance.

## Select delivery

Create separate instances of the handler above:

{{example:subscriber-handlers}}

{{example:subscriber-config}}

Pass the registration configuration to the Runner:

{{example:subscriber-runner}}

Create a context and submit a task as in [Hello World](/start/hello-world/). A custom UI Subscriber
uses the same registration; replace the EventLog instance with that implementation.

Accepted delivery can arrive in bursts after saves. It includes rejected candidates and tool
activity, not only accepted answers. Live delivery can arrive before persistence, but complete
accepted answers still wait for their save.

Count and byte limits include the active callback. Nil or zero settings select defaults: 4,096
events, 8 MiB, and 30 seconds. Negative settings are invalid. A delivery failure stops only that
registration. It does not fail the agent run or undo accepted storage.

## Read the cursors

{{example:subscriber-cursor}}

LastAcceptedEventSequenceNumber is the accepted run position when delivery stops. Execution can
continue beyond that position. Read the current stored head when you reconnect.

A callback can send an event and then fail before returning nil. Its delivery cursor does not
advance, so replay can send the event again. No cursor proves that a browser received an item. Honor
callback cancellation and release transport resources. See [Stream to a UI](/observe/streaming/) for
duplicate detection and reconnect.
