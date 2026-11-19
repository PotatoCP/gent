package examples

import (
	"context"
	"fmt"

	"github.com/rickchristie/gent"
)

// Ledger supplies independent evidence about an earlier operation. It must not repeat the action.
type Ledger interface {
	Inspect(context.Context, string) (*gent.ToolRecoveryResponse, error)
}

func recoveringTool(service *orderService, ledger Ledger) gent.Tool[*lookupInput] {
	// docs:start tool-recover
	tool := gent.DefineTool("lookup_order", service.Lookup).
		Recovery(func(ctx context.Context, saved *gent.ToolRecoveryRequest[*lookupInput]) (
			*gent.ToolRecoveryResponse, error,
		) {
			identity := saved.Identity()
			// Inspect the original operation. Do not call the original action here.
			return ledger.Inspect(ctx, identity.ToolCallId)
		}).MustBuild()
	// docs:end tool-recover
	return tool
}

func recoveredResult() *gent.ToolRecoveryResponse {
	// docs:start recovery-completed
	return &gent.ToolRecoveryResponse{
		Status: gent.ToolRecoveryCompleted,
		Execution: &gent.ToolExecutionResponse{
			Status: gent.ToolExecuted,
			Parts:  []gent.ContentPart{gent.TextPart("Shipment ABC123 was created.")},
		},
	}
	// docs:end recovery-completed
}

func unresolvedResult() *gent.ToolRecoveryResponse {
	// docs:start recovery-unresolved
	return &gent.ToolRecoveryResponse{
		Status:       gent.ToolRecoveryUnresolved,
		Instructions: "Check shipment status before requesting another shipment.",
	}
	// No recovery status authorizes Gent to repeat the original effect automatically.
	// docs:end recovery-unresolved
}

func toolIdentity(ctx context.Context) (string, error) {
	// docs:start tool-identity
	identity, ok := gent.ToolIdentityFromContext(ctx)
	if !ok {
		return "", fmt.Errorf("tool call has no managed identity")
	}
	// Send this stable ID to the external service's idempotency mechanism.
	operationId := identity.ToolCallId
	// docs:end tool-identity
	return operationId, nil
}
