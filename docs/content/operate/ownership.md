Configure services before use. Share a Runner only when its model, tools, callbacks, and other
services support concurrent calls. Each conversation has its own execution context.

## Create separate conversations

{{example:ownership-contexts}}

One context admits one active execution attempt. A concurrent attempt returns Busy. The application
owns routing, writer leases, and stale Stop command handling.

## Use read-only values

| Boundary | Ownership |
| --- | --- |
| Config and services | Configure first; read-only during use |
| Media bytes | Transfer the buffer; do not mutate it later |
| Tool response | Transfer the response to Gent |
| Hook input | Mutable effective input until callback return |
| Subscriber event | Borrow read-only, including nested values |
| Accepted result | Borrow read-only |
| Decoded state | Caller owns it |
| Successful Apply | Target owns retained batch values |
| Successful Restore | ExecutionContext owns RunState |
| Failed Apply or Restore | Caller retains unchanged input |
| Recorder capture | Independent stable RunStateSnapshot |

Decode separate batch values when loading RunState and RunView. Copy data only when you need
independent mutable data or a stable captured value. A copied slice header does not isolate a
mutable byte buffer. Do not add defensive full-history copies at every boundary.

Release joins owned workers. Do not mutate config to interrupt a run or change its limits in place.
Restore accepted state with a newly configured Runner when a change is required.
