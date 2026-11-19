package gent

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

type executionAttempt struct {
	cancel         context.CancelCauseFunc
	deliveryCtx    context.Context
	cancelDelivery context.CancelFunc
	done           chan struct{}
	stopped        bool
	closing        bool
	producers      sync.WaitGroup
	streams        map[*streamBuffer]struct{}
	workers        []*subscriberWorker
}

var errAttemptStopped = errors.New("gent: execution pause requested")

// FailAttempt is internal to Gent. A managed worker must cancel its peers before joining them
// after an operation failure. Block further acceptance as well: failed provisional work must
// remain outside saved state. The caller returns the error through its normal operation path.
// Cancellation already caused by Stop, a limit, or the owner keeps its separate contract.
func FailAttempt(execCtx *ExecutionContext, err error) error {
	ctx := execCtx.Context()
	if onlyCancellation(err, ctx.Err(), context.Cause(ctx)) {
		return err
	}
	err = fmt.Errorf("gent: managed operation in run %q turn %q context %q: %w",
		execCtx.RunId(), execCtx.TurnId(), execCtx.ContextId(), err)
	return execCtx.stopRecording(err)
}

// Config returns the shared read-only configuration. Do not change it or reachable values while
// in use. Restore with a new Runner to change configuration without changing accepted progress.
func (execCtx *ExecutionContext) Config() *Config { return execCtx.config }

// TurnId identifies the accepted task across continued attempts. It changes on NewTurn admission.
func (execCtx *ExecutionContext) TurnId() string {
	execCtx.mu.RLock()
	defer execCtx.mu.RUnlock()
	return execCtx.turnId
}

// Stop requests a pause of the current active attempt without completing its turn. It is
// nonblocking and idempotent. An idle call does nothing and cannot stop a later attempt. The
// running
// NewTurn or Continue call owns cleanup and the pause save; Stop alone proves no persistence.
// A callback can call Stop. Applications must route remote commands to the intended run and turn,
// serialize that check with admission, and discard stale commands. Stop has no remote target ID.
func (execCtx *ExecutionContext) Stop() {
	execCtx.mu.Lock()
	defer execCtx.mu.Unlock()
	attempt := execCtx.attempt
	if attempt == nil || attempt.closing || attempt.stopped {
		return
	}
	attempt.stopped = true
	attempt.cancel(errAttemptStopped)
}

// Release permanently disposes of the handle and joins its active attempt. It is idempotent and
// makes no terminal save. Accepted results remain readable; restore accepted state to execute
// again. Call from the owner, never synchronously from a callback whose completion it must join.
// Cooperative callbacks can use Stop to request a pause without waiting on their own completion.
func (execCtx *ExecutionContext) Release() {
	execCtx.mu.Lock()
	execCtx.released = true
	attempt := execCtx.attempt
	if attempt != nil {
		attempt.cancel(errExecutionReleased)
		attempt.cancelDelivery()
	}
	execCtx.mu.Unlock()
	if attempt != nil {
		<-attempt.done
	}
}

func (execCtx *ExecutionContext) beginAttempt(owner any, continuing bool) (
	*executionAttempt, *ExecutionResult, error,
) {
	execCtx.mu.Lock()
	defer execCtx.mu.Unlock()
	// Busy takes precedence and cannot touch the active owner's resources or provisional data.
	if execCtx.attempt != nil {
		return nil, &ExecutionResult{Status: ExecutionBusy}, nil
	}
	if execCtx.released || execCtx.unusable {
		return nil, nil, fmt.Errorf("gent: run %q needs accepted-state restoration before execution",
			execCtx.RunId())
	}
	if execCtx.runner != owner {
		return nil, nil, fmt.Errorf("gent: run %q belongs to another Runner; restore with this Runner",
			execCtx.RunId())
	}
	if err := execCtx.ownerCtx.Err(); err != nil {
		return nil, nil, fmt.Errorf("gent: run %q owner context ended: %w", execCtx.RunId(),
			errors.Join(err, context.Cause(execCtx.ownerCtx)))
	}
	if continuing && !execCtx.turnOpen {
		return nil, &ExecutionResult{Status: ExecutionNoActiveTurn}, nil
	}
	ctx, cancel := context.WithCancelCause(execCtx.ownerCtx)
	deliveryCtx, cancelDelivery := context.WithCancel(execCtx.ownerCtx)
	attempt := &executionAttempt{cancel: cancel, deliveryCtx: deliveryCtx,
		cancelDelivery: cancelDelivery, done: make(chan struct{})}
	execCtx.goCtx, execCtx.cancel = ctx, cancel
	execCtx.attempt = attempt
	execCtx.exceededLimit, execCtx.limitDetails = nil, nil
	execCtx.result, execCtx.err = nil, nil
	execCtx.terminationReason = ""
	return attempt, nil, nil
}

func (execCtx *ExecutionContext) startDelivery(attempt *executionAttempt) error {
	execCtx.run.mu.Lock()
	execCtx.run.attempt = randomIdentity()
	attemptId := execCtx.run.attempt
	execCtx.run.mu.Unlock()
	for _, registration := range execCtx.config.Subscribers {
		worker, err := newSubscriberWorker(attempt.deliveryCtx, execCtx.RunId(), attemptId,
			registration, func() uint64 {
				execCtx.run.mu.RLock()
				defer execCtx.run.mu.RUnlock()
				return execCtx.run.view.LastAppliedEventSequenceNumber
			})
		if err != nil {
			return err
		}
		attempt.workers = append(attempt.workers, worker)
	}
	execCtx.run.mu.Lock()
	execCtx.run.subscribers = attempt.workers
	execCtx.run.mu.Unlock()
	// A resumed attempt is visible through the next approved save. It adds no savepoint.
	// The envelope distinguishes retried live previews from the earlier accepted attempt.
	if execCtx.turnOpen {
		return execCtx.recordAttemptEvent("attempt_started", nil)
	}
	return nil
}

func (execCtx *ExecutionContext) recordAttemptEvent(kind string, payload any) error {
	value, err := execCtx.Codecs().Clone(payload)
	if err != nil {
		return execCtx.stopRecording(err)
	}
	event := &RecordedEvent{Type: kind, ContextId: execCtx.ContextId(),
		Iteration: execCtx.Iteration(), AttemptId: execCtx.AttemptId(), Payload: value}
	execCtx.run.mu.Lock()
	err = execCtx.run.queueEvent(event)
	execCtx.run.mu.Unlock()
	if err != nil {
		return execCtx.stopRecording(err)
	}
	return nil
}

func (execCtx *ExecutionContext) endAttempt(attempt *executionAttempt, failed bool) {
	execCtx.mu.Lock()
	attempt.closing = true
	attempt.cancel(context.Canceled)
	if failed {
		execCtx.unusable = true
	}
	execCtx.mu.Unlock()
	execCtx.closeAttemptStreams(attempt)
	attempt.producers.Wait()
	for _, worker := range attempt.workers {
		if attempt.deliveryCtx.Err() != nil {
			worker.abort(SubscriberClosed, nil)
		} else {
			worker.close()
		}
	}
	for _, worker := range attempt.workers {
		<-worker.done
	}
	attempt.cancelDelivery()
	execCtx.run.mu.Lock()
	execCtx.run.subscribers = nil
	execCtx.run.mu.Unlock()
	execCtx.mu.Lock()
	execCtx.attempt = nil
	close(attempt.done)
	execCtx.mu.Unlock()
}

func (execCtx *ExecutionContext) trackProducer() (func(), error) {
	execCtx.mu.Lock()
	defer execCtx.mu.Unlock()
	attempt := execCtx.attempt
	if attempt == nil {
		return func() {}, nil
	}
	if attempt.closing || execCtx.goCtx.Err() != nil {
		return nil, fmt.Errorf("gent: run %q attempt cannot start another producer: %w",
			execCtx.RunId(), context.Cause(execCtx.goCtx))
	}
	attempt.producers.Add(1)
	return attempt.producers.Done, nil
}

// The attempt owns delivery as well as production. A custom Loop can stop reading early, and
// saved model replay has a drain worker even though it has no active provider producer.
func (execCtx *ExecutionContext) trackStream(stream *streamBuffer) {
	execCtx.mu.Lock()
	attempt := execCtx.attempt
	if attempt == nil {
		execCtx.mu.Unlock()
		return
	}
	if attempt.closing {
		execCtx.mu.Unlock()
		stream.Close()
		return
	}
	if attempt.streams == nil {
		attempt.streams = make(map[*streamBuffer]struct{})
	}
	stream.onDrained = func() {
		execCtx.mu.Lock()
		delete(attempt.streams, stream)
		execCtx.mu.Unlock()
	}
	attempt.streams[stream] = struct{}{}
	execCtx.mu.Unlock()
}

func (execCtx *ExecutionContext) closeAttemptStreams(attempt *executionAttempt) {
	execCtx.mu.Lock()
	streams := attempt.streams
	attempt.streams = nil
	execCtx.mu.Unlock()
	// closing is already set. Any producer that returns a stream later closes it at registration.
	for stream := range streams {
		stream.Close()
	}
}

// Join producers before a final or pause save so observed completion and usage cannot arrive
// after that save. The attempt still owns admission until all delivery cleanup has finished.
func (execCtx *ExecutionContext) joinProducers() {
	execCtx.mu.Lock()
	attempt := execCtx.attempt
	attempt.closing = true
	attempt.cancel(context.Canceled)
	execCtx.mu.Unlock()
	execCtx.closeAttemptStreams(attempt)
	attempt.producers.Wait()
}
