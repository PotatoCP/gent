package gent

import (
	core "github.com/rickchristie/gent/internal/core"
)

// Tool is an operation with typed input and an explicit response. Gent validates input, saves
// the effective input after hooks, invokes Call, and prepares a correlated model reply. The
// author supplies domain data and guidance, without serialization or provider message plumbing.
// Configure tools before sharing them between runs. Methods must support concurrent calls.
type Tool[I any] = core.Tool[I]

// ToolStatus describes an accepted execution response. It does not classify business success.
// Empty status is invalid. Interrupted-operation uncertainty belongs in ToolRecoveryStatus.
type ToolStatus = core.ToolStatus

// ToolExecuted means the tool returned a domain response. The data determines domain success.
const ToolExecuted ToolStatus = core.ToolExecuted

// ToolBlocked means a required condition prevented the action. Instructions explain the next step.
const ToolBlocked ToolStatus = core.ToolBlocked

// ToolInvalidArguments means input was rejected. It does not increment tool-error statistics.
const ToolInvalidArguments ToolStatus = core.ToolInvalidArguments

// ToolExecutionResponse supplies content and guidance for a requested execution. Gent can also
// construct it before invocation for input rejection or a blocked action. Status shows the case.
// After return, the response and its parts are owned by Gent and must not be changed.
type ToolExecutionResponse = core.ToolExecutionResponse
