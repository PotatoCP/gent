package gent

import (
	core "github.com/rickchristie/gent/internal/core"
)

// Output defines the answer contract. Configure requests native controls without changing the
// caller's messages. Process receives a complete candidate, after native completion checks.
// Implementations must support concurrent calls after configuration is complete.
type Output[T any] = core.Output[T]

// OutputResult separates expected rejection from an operation failure. Acceptance supplies Value
// and matching delivery Content, with no Steering. Rejection requires useful Steering and leaves
// Value and Content unused. An error from Process stops execution instead of requesting correction.
type OutputResult[T any] = core.OutputResult[T]

type OutputValidationResult = core.OutputValidationResult

// OutputCustomValidator validates a built-in output after its local checks. It must not change
// value, including referenced maps, slices, or pointers. Configuration must supply a rule-set name.
type OutputCustomValidator[T any] = core.OutputCustomValidator[T]
