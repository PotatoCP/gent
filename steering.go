package gent

import (
	core "github.com/rickchristie/gent/internal/core"
)

// SteeringMessages supplies authored model guidance for one execution. Empty fields use defaults.
// Overrides replace the default sentence, not required context, schema, or tool Instructions.
// These are plain text: phrases such as FULL STOP do not act as framework execution commands.
type SteeringMessages = core.SteeringMessages

// DefaultSteeringMessages returns the built-in model guidance. Errors do not prove absence of
// effects. Recovery reports leave the next action to the model and never promise a safe retry.
func DefaultSteeringMessages() SteeringMessages {
	return core.DefaultSteeringMessages()
}

// PTCIntegerMode selects how the program tool represents integer values.
type PTCIntegerMode = core.PTCIntegerMode

// PTCIntegerNumber is the default. JavaScript numbers can round integers outside the safe range.
const PTCIntegerNumber PTCIntegerMode = core.PTCIntegerNumber

// PTCIntegerBigInt represents all captured integers as bigint, including small integers.
const PTCIntegerBigInt PTCIntegerMode = core.PTCIntegerBigInt

// PrepareExecutionParts selects safe accepted content and appends instructions once. Input
// rejection instructions must already contain safe correction details and the complete schema.
// The returned slice is new because it adds guidance; its read-only payloads share ownership.
func PrepareExecutionParts(response *ToolExecutionResponse,
	steering SteeringMessages,
) []ContentPart {
	return core.PrepareExecutionParts(response, steering)
}

// PrepareRecoveryParts retains recovered content, a recovery note, evidence, and both sets of
// instructions. It does not repeat or schedule the original action.
func PrepareRecoveryParts(response *ToolRecoveryResponse, steering SteeringMessages) []ContentPart {
	return core.PrepareRecoveryParts(response, steering)
}
