package trace

import "github.com/rickchristie/gent"

// RestoreSequencer rebuilds display state from accepted records without restoring execution.
func RestoreSequencer(saved gent.RunView, cfg Config) (*Sequencer, error) {
	registry := cfg.Codecs
	if registry == nil {
		registry = gent.NewCodecRegistry()
	}
	if err := gent.ValidateBundle(saved, registry); err != nil {
		return nil, err
	}
	seq, err := NewSequencer(saved.RunId, cfg)
	if err != nil {
		return nil, err
	}
	if err := seq.observeEvents(saved.Activity, registry); err != nil {
		return nil, err
	}
	return seq, nil
}

func (s *Sequencer) observeEvents(
	events []*gent.RecordedEvent, registry *gent.CodecRegistry,
) error {
	s.observeMu.Lock()
	defer s.observeMu.Unlock()
	for _, recorded := range events {
		s.mu.Lock()
		if s.runId == "" && recorded != nil && recorded.RunId != "" {
			s.runId, s.snapshot.RunId = recorded.RunId, recorded.RunId
		}
		s.mu.Unlock()
		event, err := s.project(recorded, registry)
		if err != nil {
			return err
		}
		s.record(event)
	}
	return nil
}

func (s *Sequencer) applyAttempt(event *Event) {
	if event.Type != EventTypeAttemptStarted && event.Type != EventTypeAttemptInterrupted {
		return
	}
	if event.Type == EventTypeAttemptStarted {
		s.snapshot.Status, s.snapshot.CompletedTs = RunStatusRunning, nil
		if ctx := s.contextById[event.ContextId]; ctx != nil {
			ctx.Status, ctx.CompletedTs = StepStatusRunning, nil
		}
	}
	interrupted := func(attempt string) bool {
		if event.Type == EventTypeAttemptStarted {
			return attempt != event.AttemptId
		}
		return attempt == event.AttemptId
	}
	withinContext := func(id string) bool { return id == event.ContextId }
	resumeIteration := false
	for _, iteration := range s.snapshot.Iterations {
		if iteration.ContextId == event.ContextId && iteration.Iteration == event.Iteration &&
			(iteration.Status == StepStatusRunning || iteration.Status == StepStatusInterrupted) {
			resumeIteration = true
		}
	}
	for _, call := range s.snapshot.ModelCalls {
		if call.Status == StepStatusRunning && interrupted(call.AttemptId) &&
			withinContext(call.ContextId) {
			call.Status = StepStatusInterrupted
		}
	}
	for _, call := range s.snapshot.ToolCalls {
		if call.Status == StepStatusRunning && interrupted(call.AttemptId) &&
			withinContext(call.ContextId) {
			call.Status = StepStatusInterrupted
		}
	}
	for _, iteration := range s.snapshot.Iterations {
		if iteration.Status == StepStatusRunning && interrupted(iteration.AttemptId) &&
			withinContext(iteration.ContextId) {
			iteration.Status = StepStatusInterrupted
		}
	}
	if event.Type == EventTypeAttemptStarted && resumeIteration {
		s.applyIterationStarted(event)
	}
}
