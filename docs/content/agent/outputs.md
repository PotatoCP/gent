`Output[T]` defines the accepted answer type. ReAct uses `TextOutput` by default. Use
`react.NewWithOutput` to select another contract. Local validation always runs, even when the
provider applies native output constraints.

## Return a Go value

Define the answer shape:

{{example:output-type}}

Configure the loop with the existing `model`:

{{example:output-json}}

## Supply a JSON schema

{{example:output-schema}}

Use this `out` with `react.NewWithOutput(model, out)` and `executor.NewRunner`, as in the preceding
example. `NewTurn` returns `Answer` for both JSON output contracts.

Schema construction can fail before execution. Check the returned error. Do not use an invalid
schema and rely on the model to repair application configuration.

## Add validation

{{example:output-validator}}

Pass this `out` to `react.NewWithOutput` before creating the Runner. `NewTurn` then returns a string
that passed both the built-in checks and this validator.

A rejected candidate needs useful `Steering`. ReAct keeps the rejected candidate and correction
selected through the next main model call. Normal limits apply to this work. A validator error ends
the attempt and follows [Execution control and failures](/run/control/).

## Return images

{{example:output-image}}

Use this `out` with `react.NewWithOutput` and create the Runner in the same way. NewTurn returns
`[]gent.BinaryContent`. Process the accepted image parts after checking the execution result.

`ImageOutput` returns ordered PNG or JPEG parts. It checks complete files and decoded pixel limits.
It does not repair or resize images. Select a provider and model that support the requested output.

## Implement an output contract

This contract removes surrounding space. It returns matching typed and display values.

{{example:output-custom}}

Connect the custom contract to the loop:

{{example:output-custom-runner}}

Use this Runner with the context and task calls from [Hello World](/start/hello-world/).
Its accepted string and display content both have surrounding space removed.

`Configure` sets native controls. `Process` checks the candidate. `Instructions` supplies optional
caller-authored guidance. Gent saves accepted output before publishing `accepted_answer`. Original
candidates remain in the archive, with source links to the processed answer. Restoration reads
accepted output without running the processor or validator again.
