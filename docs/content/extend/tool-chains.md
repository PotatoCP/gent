DefineTool is suitable for most tools. Implement Tool[I] directly when a tool needs its own state or
method implementations. It includes metadata, schemas, Call, and Recover.

## Select advertised tools

Embed the registry to retain its validation and recovery behavior:

{{example:chain-custom}}

This changes visibility, not authorization. The handler must still check whether the requested
action is permitted. A model's lack of an advertised definition is not an access-control boundary.

## Give the custom chain to the Runner

Here `tool` is a built tool named `lookup_order`:

{{example:custom-chain-runner}}

Create a context and call NewTurn as in [Hello World](/start/hello-world/). Gent still uses the
embedded registry for managed calls, validation, and recovery.

## Implement the managed chain contract

NativeTools selects definitions. PrepareCalls validates requests and proposes a plan. ExecuteNext
runs one managed step, with effective input saved before execution and result accepted afterward.
RegisterCodecs supplies types needed to restore saved work. Recover binds the saved original input
to the current recovery handler.

SearchableToolChain supplies local discovery. ParallelToolChain explicitly declares support for
independent concurrent plans. Keep plan identities separate. Do not bypass the managed tool path
when adding another scheduler or an execution wrapper.
