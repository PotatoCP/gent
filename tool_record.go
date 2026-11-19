package gent

import (
	core "github.com/rickchristie/gent/internal/core"
)

// ToolResponseKind selects the accepted execution, recovery, or safe error path. It is metadata
// for Gent and provider adapters. Gent does not print the kind as model-facing content.
type ToolResponseKind = core.ToolResponseKind

// ToolResponseExecution has an accepted execution response, including pre-call rejection.
const ToolResponseExecution ToolResponseKind = core.ToolResponseExecution

// ToolResponseRecovery has an accepted report about an interrupted call.
const ToolResponseRecovery ToolResponseKind = core.ToolResponseRecovery

// ToolResponseCallError has safe guidance for an operation with no accepted response.
const ToolResponseCallError ToolResponseKind = core.ToolResponseCallError

// ToolResponseRecoveryError has safe guidance for a failed check of the original operation.
const ToolResponseRecoveryError ToolResponseKind = core.ToolResponseRecoveryError

// ModelToolCallResponse is the prepared reply to one model-requested tool call. A record builder
// creates it together with the accepted outcome. Only safe content belongs in its parts. The
// provider adapter encodes content and call IDs without exposing framework metadata as JSON.
// Getters return read-only views. Retain the response while using a view.
type ModelToolCallResponse = core.ModelToolCallResponse

// ToolCallRecord owns the input, accepted outcome, original diagnostics, and prepared model reply.
// Only Response belongs in model input. Completed records remain readable without old tool types.
// Returned pointers, maps, and slices are read-only and stay valid while the record is retained.
type ToolCallRecord = core.ToolCallRecord

// ToolCallRecordBuilder joins one accepted outcome, private snapshots, and safe prepared parts.
// It does not call tools, select steering, encode provider messages, save state, or update stats.
// Setters take ownership of retained values. Use a builder for one record in one goroutine.
type ToolCallRecordBuilder = core.ToolCallRecordBuilder

// NewToolCallRecordBuilder sets Gent's identity and target. ModelReply supplies provider identity.
func NewToolCallRecordBuilder(callID, attemptID, target string) *ToolCallRecordBuilder {
	return core.NewToolCallRecordBuilder(callID, attemptID, target)
}
