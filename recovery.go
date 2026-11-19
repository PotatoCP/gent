package gent

import (
	"context"
	core "github.com/rickchristie/gent/internal/core"
	"reflect"
)

// ToolRecoveryStatus states the evidence found about an interrupted call. No value permits
// automatic execution. Gent sends the report to the model, which selects the next action.
type ToolRecoveryStatus = core.ToolRecoveryStatus

// ToolRecoveryCompleted means the original response is available, not that it remains current.
const ToolRecoveryCompleted ToolRecoveryStatus = core.ToolRecoveryCompleted

// ToolRecoveryNotStarted means evidence establishes that the original action did not start.
const ToolRecoveryNotStarted ToolRecoveryStatus = core.ToolRecoveryNotStarted

// ToolRecoveryUnresolved means the outcome cannot be confirmed, including an active remote job.
const ToolRecoveryUnresolved ToolRecoveryStatus = core.ToolRecoveryUnresolved

// ToolRecoveryRequest retains original identity, arguments, and effective typed input. The views
// are read-only. Gent restores I before invoking Recover and does not run input hooks again.
// A missing binding or incompatible input becomes a recovery error, not NotStarted.
type ToolRecoveryRequest[I any] = core.ToolRecoveryRequest[I]

// NewToolRecoveryRequest takes ownership of args and input. Tools normally receive a request
// from Gent; custom chains use this constructor when binding the saved input to the current tool.
func NewToolRecoveryRequest[I any](identity ToolIdentity, name string, args map[string]any,
	input I,
) *ToolRecoveryRequest[I] {
	return core.NewToolRecoveryRequest[I](identity, name, args, input)
}

// ToolRecoveryResponse reports evidence without directing execution. Completed requires Execution;
// NotStarted and Unresolved forbid it. Parts and Instructions describe the check independently.
// Returning a report and an error is invalid. Gent saves both privately and uses safe guidance.
type ToolRecoveryResponse = core.ToolRecoveryResponse

// ToolRecoveryFunc is a typed inspection handler. It must be safe to repeat after interruption.
type ToolRecoveryFunc[I any] = core.ToolRecoveryFunc[I]

// ValidateToolExecution checks the response contract, without inspecting domain success or schema.
func ValidateToolExecution(response *ToolExecutionResponse) error {
	return core.ValidateToolExecution(response)
}

// ValidateToolRecovery checks status and field pairing. It never authorizes a retry.
func ValidateToolRecovery(response *ToolRecoveryResponse) error {
	return core.ValidateToolRecovery(response)
}

// BindToolRecoveryInput constructs the registered handler's typed request from saved input.
// Chains call it only during Recover, using the context supplied by ExecutionContext.RecoverTool.
// It does not run input hooks or invoke the original tool. Missing types return a per-call error.
func BindToolRecoveryInput(ctx context.Context, typ reflect.Type,
	request *ToolRecoveryRequest[EncodedValue],
) (any, error) {
	return core.BindToolRecoveryInput(ctx, typ, request)
}
