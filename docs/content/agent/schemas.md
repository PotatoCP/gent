Gent infers tool input schemas from Go structs and JSON field names. Fields are required by default.
Use field rules when the inferred type needs more constraints.

## Add field rules

`service.Lookup` accepts a pointer to a struct with the JSON field `id`.

{{example:schema-fields}}

`Strict(true)` requires the provider to enforce the tool's input schema. Gemini, Vertex AI, and
Ollama reject this setting during `PrepareRequest`, after the tool catalog is resolved. Gent
validates tool arguments locally before the handler and after mutable input Hooks.

## Give the tool to the Runner

Register the built `tool` before creating an execution context:

{{example:schema-runner}}

The schema variants below use the same registration. Run a task with this Runner as shown in
[Hello World](/start/hello-world/). Gent applies the tool's input schema before its handler runs.

## Make a field optional

{{example:schema-optional}}

{{example:schema-optional-build}}

An absent optional field receives its Go zero value. Use a pointer when your application must
separate absence from an explicit zero.

## Supply a complete schema

{{example:schema-explicit}}

The top-level input schema must describe an object. Use an inferred `struct{}` for no arguments. The
builder rejects invalid schemas during setup.

## Describe tool output

{{example:schema-output}}

The tool output schema is optional model guidance. It does not validate handler returns. Use
[Outputs](/agent/outputs/) to validate the agent's final answer. Schema helpers also support
formats, enums, lengths, and collection constraints; see the [schema API](/reference/api/schema/).
