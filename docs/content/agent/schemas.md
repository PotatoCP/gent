Gent infers tool input schemas from Go structs and JSON field names. Fields are required by default.
Use field rules when the inferred type needs more constraints.

## Add field rules

`service.Lookup` accepts a pointer to a struct with the JSON field `id`.

{{example:schema-fields}}

`Strict(true)` requests provider argument enforcement where supported. Gent still performs local
validation before the handler and after mutable input Hooks.

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
