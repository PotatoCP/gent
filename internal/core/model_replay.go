package gent

import "fmt"

// The checkpoint starts the replay cursor. Accepted progress sets its end and event limit.
// Keep the allocation counter at its accepted value so new calls cannot reuse saved IDs.
type modelReplay struct {
	next                uint64
	end                 uint64
	eventSequenceNumber uint64
	iteration           int
}

func (execCtx *ExecutionContext) modelCallId(id string) (string, error) {
	if id != "" {
		return id, nil
	}
	execCtx.mu.Lock()
	defer execCtx.mu.Unlock()
	if execCtx.iteration == execCtx.modelReplay.iteration &&
		execCtx.modelReplay.next < execCtx.modelReplay.end {
		execCtx.modelReplay.next++
		return fmt.Sprintf("%s:model:%d", execCtx.contextId, execCtx.modelReplay.next), nil
	}
	return execCtx.allocateCallId("model", &execCtx.nextModelCallSeq)
}

func (execCtx *ExecutionContext) replayModelCall(id string) (Stream, error) {
	if execCtx.modelReplay.eventSequenceNumber == 0 {
		return nil, nil
	}
	execCtx.run.mu.RLock()
	evidence := execCtx.run.view.Activity
	execCtx.run.mu.RUnlock()
	iteration := execCtx.Iteration()
	for i := len(evidence) - 1; i >= 0; i-- {
		event := evidence[i]
		if event.EventSequenceNumber > execCtx.modelReplay.eventSequenceNumber ||
			event.ContextId != execCtx.ContextId() ||
			event.Iteration != iteration || event.ModelCallId != id {
			continue
		}
		if event.Type == "model_call_started" {
			// The last attempt did not finish. Retry with its logical ID.
			return nil, nil
		}
		if event.Type != "model_call_finished" {
			continue
		}
		saved := event.Payload.(*AfterModelCallEvent)
		if saved.Response == nil || saved.Error != nil {
			return nil, nil
		}
		return execCtx.savedModelStream(evidence[:i], saved)
	}
	return nil, nil
}

func (execCtx *ExecutionContext) savedModelStream(
	events []*RecordedEvent, saved *AfterModelCallEvent,
) (Stream, error) {
	var chunks []StreamChunk
	for i := len(events) - 1; i >= 0; i-- {
		event := events[i]
		if event.ContextId != saved.ContextId || event.ModelCallId != saved.ModelCallId {
			continue
		}
		if event.Type == "model_call_started" {
			break
		}
		if event.Type != "model_stream_chunk" {
			continue
		}
		// Replay borrows already validated frozen payloads. The stream owns its delivery copy.
		chunks = append(chunks, *event.Payload.(*StreamChunk))
	}
	stream := newStreamBuffer()
	for i := len(chunks) - 1; i >= 0; i-- {
		// These chunks were already accepted. Do not publish them or add usage a second time.
		stream.Send(chunks[i])
	}
	stream.Complete(saved.Response, nil)
	return stream, nil
}
