package gent

import (
	"errors"
	"fmt"
	"math"
	"time"
)

// queueEvent runs with run.mu held. Assign the live position once, before either delivery or
// saving. All contexts use this lock, so frozen events cannot overtake one another.
func (run *executionRun) queueEvent(event *RecordedEvent) error {
	if run.err != nil {
		return run.err
	}
	if err := validateRecordedPayload(event); err != nil {
		return err
	}
	if run.nextEventSequenceNumber == math.MaxUint64 {
		return errors.New("gent: event sequence exhausted")
	}
	candidate := *event
	candidate.RunId = run.id
	candidate.TurnId = run.root.TurnId()
	candidate.EventSequenceNumber = run.nextEventSequenceNumber + 1
	if candidate.Ts.IsZero() {
		candidate.Ts = time.Now()
	}
	// The event is a saved value. Remove process-local monotonic clock data before live delivery
	// so the frozen envelope and its decoded archive form have the same timestamp representation.
	candidate.Ts = candidate.Ts.Round(0)
	data, err := run.codecs.Marshal(&candidate)
	if err != nil {
		return err
	}
	if len(
		run.pending,
	) >= run.policy.MaxPendingEvents || len(
		data,
	) > run.policy.MaxPendingBytes-run.pendingBytes {
		return fmt.Errorf("gent: run %q recording allowance exceeded: events %d + 1, limit %d; "+
			"encoded bytes %d + %d, limit %d", run.id, len(run.pending), run.policy.MaxPendingEvents,
			run.pendingBytes, len(data), run.policy.MaxPendingBytes)
	}
	// Only an event that fits receives a sequence and live delivery. Bounds never trigger a save
	// outside an approved boundary, and an overflowing event cannot create a sequence gap.
	*event = candidate
	run.nextEventSequenceNumber = event.EventSequenceNumber
	run.pending = append(run.pending, event)
	run.pendingSizes = append(run.pendingSizes, len(data))
	run.pendingBytes += len(data)
	if event.Type == "execution_finished" || event.Type == "accepted_answer" ||
		event.Type == "execution_paused" {
		run.deferLive = true
	}
	if !run.deferLive {
		run.publishLive(event, len(data))
	}
	return nil
}

// publishLive runs under run.mu. It only copies frozen data and queues it; application consumers
// run on their own goroutines and cannot delay a save or another subscriber.
func (run *executionRun) publishLive(event *RecordedEvent, size int) {
	if event.EventSequenceNumber <= run.liveEventSequenceNumber {
		return
	}
	run.liveEventSequenceNumber = event.EventSequenceNumber
	for _, worker := range run.subscribers {
		if worker.delivery == SubscriberLive {
			worker.send(event, size)
		}
	}
}

func (execCtx *ExecutionContext) flushPendingLimit() error {
	return execCtx.RecordingError()
}
