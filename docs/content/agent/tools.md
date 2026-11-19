Tools let the model request work from your application. Define a tool, register it, and pass the
chain to the Runner. `model` is already configured in the first example.

## Add a tool

{{example:tools-add}}

Execution is the same as [Hello World](/start/hello-world/). Use a task such as `Use add to
calculate 19 + 23.` Gent derives the input schema from `AddInput` and checks the model's arguments
before calling the function.

## Use a service method

`service.Lookup` is an application method with the typed tool-call signature. This example uses
`Build` so invalid setup can be reported to the application.

{{example:tools-method}}

Use `MustBuild` for a fixed definition when invalid setup must stop startup. Configure a tool before
sharing it between runs. Its handler must support concurrent calls if those runs can overlap.

## Return data and guidance

Inside a tool handler, return the computed `sum` as structured data:

{{example:tools-data}}

`ToolExecuted` means a normal tool response. The returned domain data determines business success.
`Instructions` supplies additional model guidance and is included once in the prepared reply.

## Block an action

After your application determines that approval is absent, return before performing the action:

{{example:tools-blocked}}

`Policy` on a tool definition supplies model guidance. It does not enforce approval or
authorization. Keep those checks in your handler. A blocked action does not end the agent run.

## Reject domain input

Use this result when input passes its schema but fails a domain rule:

{{example:tools-invalid}}

## Report an unexpected error

{{example:tools-error}}

Do not return both a response and an error. Gent keeps raw errors and rejected returns in private
diagnostic evidence. Safe guidance goes to the model. This error ends the tool operation, not the
agent run. See [Tool recovery](/storage/tool-recovery/) for uncertain external effects.

## Add usage guidance

Use the existing application service:

{{example:tools-policy}}
