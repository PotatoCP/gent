Hooks run synchronously before model or tool input is frozen. They can change effective input.
Subscribers only observe read-only evidence. Persistent loop changes belong in LoopData and history.

## Change a model request

{{example:hooks-model}}

## Normalize tool input

This handler uses the `lookupInput` type from the tool's definition:

{{example:hooks-tool}}

Gent validates effective tool input after the Hook. Do not change operation identities or retain
mutable access after return. The archive keeps the effective input that the operation received.

## Compose and fail

To compose callbacks, call the first, check its error, then call the second in the same configured
function. A returned error blocks dependent work and propagates to the application. A later restore
can run a Hook again. Use managed tools for external effects that need recovery.

Shared callbacks must support concurrent runs. The supported mutation stages are BeforeModelCall and
BeforeToolCall. Lifecycle observation does not grant permission to mutate execution.
