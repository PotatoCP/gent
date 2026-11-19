package gent

import "time"

// ToolSearchTrace is private diagnostic evidence for local searches used by the next request.
// It is never model content. An unsaved search trace can be lost on interruption; the local
// catalog read can be repeated without a separate search recovery state machine.
type ToolSearchTrace struct {
	Query     string
	QueryType string
	Names     []string
	Duration  time.Duration
	Flow      string
	Error     error
}

// RecordToolSearch queues read-only diagnostics for the next BeforeModelCallEvent. It does not
// add a conversation message or force a separate checkpoint. Normal progress-save rules apply.
func (execCtx *ExecutionContext) RecordToolSearch(trace *ToolSearchTrace) {
	execCtx.mu.Lock()
	execCtx.toolSearches = append(execCtx.toolSearches, trace)
	execCtx.mu.Unlock()
}

func (execCtx *ExecutionContext) takeToolSearches() []*ToolSearchTrace {
	execCtx.mu.Lock()
	defer execCtx.mu.Unlock()
	result := execCtx.toolSearches
	execCtx.toolSearches = nil
	return result
}
