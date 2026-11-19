package gent

// SetFinalContentSource links the next successful terminal result to its provisional messages.
// The executor supplies AgentLoopResult.ContentSource. A custom executor may call this directly.
func (execCtx *ExecutionContext) SetFinalContentSource(source *ContentSource) {
	execCtx.mu.Lock()
	defer execCtx.mu.Unlock()
	execCtx.finalContentSource = cloneContentSource(source)
}

// queueFinalContent runs under run.mu at the terminal save boundary. It never flushes by itself:
// Acceptance without the terminal checkpoint could publish an answer that a retry repeats.
func (execCtx *ExecutionContext) queueFinalContent() error {
	execCtx.mu.RLock()
	reason, result := execCtx.terminationReason, execCtx.finalResult
	source := cloneContentSource(execCtx.finalContentSource)
	stamp := execCtx.endTime
	execCtx.mu.RUnlock()
	if reason != TerminationSuccess {
		return nil
	}
	chunk := StreamChunk{Content: []ContentPart{&AssistantContent{
		Type: AssistantContentFinalAnswer, MessageId: execCtx.turnId + ":answer", Content: result}}}
	modelCallId := ""
	if source != nil {
		modelCallId = source.ModelCallId
		chunk.SourceMessageIds = source.MessageIds
	}
	value, err := cloneOwned(execCtx.run.codecs, &chunk)
	if err != nil {
		return err
	}
	return execCtx.run.queueEvent(&RecordedEvent{Type: "accepted_answer",
		ContextId: execCtx.ContextId(),
		Iteration: execCtx.Iteration(), AttemptId: execCtx.run.attempt, ModelCallId: modelCallId,
		Ts: stamp, Payload: value})
}
