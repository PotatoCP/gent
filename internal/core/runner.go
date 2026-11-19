package gent

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"
)

// Runner is the internal implementation of executor.Runner. The public execution family stays
// in executor; keeping the state owner here avoids a second public setup or restore path.
type Runner[T any] struct {
	loop   Loop[T]
	config *Config
}

func NewRunner[T any](loop Loop[T], config *Config) *Runner[T] {
	return &Runner[T]{loop: loop, config: config}
}

func (runner *Runner[T]) NewExecutionContext(
	ctx context.Context, name string,
) (*ExecutionContext, error) {
	config, err := resolveConfig(runner.config)
	if err != nil {
		return nil, fmt.Errorf("gent: prepare execution configuration: %w", err)
	}
	execCtx := &ExecutionContext{ownerCtx: ctx, config: config, runner: runner,
		goCtx: ctx, cancel: func(error) {}, name: name, data: config.NewLoopData(),
		limits: config.Limits, startTime: time.Now()}
	prepared := false
	defer func() {
		if !prepared {
			execCtx.Release()
		}
	}()
	execCtx.stats = newExecutionStatsWithContext(execCtx)
	execCtx.initializeHistory(config)
	if config.SteeringMessages != nil {
		execCtx.steeringMessages = *config.SteeringMessages
	}
	if err := execCtx.RecordingError(); err != nil {
		return nil, err
	}
	if err := PrepareOutput(execCtx, runner.loop.Output()); err != nil {
		return nil, fmt.Errorf("gent: configure output for run %q: %w", execCtx.RunId(), err)
	}
	if configured, ok := any(
		runner.loop,
	).(interface {
		ConfigureExecution(
			*ExecutionContext,
		) error
	}); ok {
		if err := configured.ConfigureExecution(execCtx); err != nil {
			return nil, fmt.Errorf("gent: configure Loop %T for run %q: %w", runner.loop,
				execCtx.RunId(), err)
		}
	}
	if config.ToolChain != nil {
		if err := config.ToolChain.RegisterCodecs(config.Codecs); err != nil {
			return nil, fmt.Errorf("gent: configure ToolChain codecs: %w", err)
		}
	}
	components := []struct {
		name  string
		value any
	}{{"data", execCtx.data}, {"loop", runner.loop},
		{"compaction_trigger", config.CompactionTrigger},
		{"compaction_strategy", config.CompactionStrategy}}
	for _, component := range components {
		if state, ok := component.value.(CheckpointState); ok {
			if err := execCtx.BindCheckpointState(component.name, state); err != nil {
				return nil, err
			}
		}
	}
	prepared = true
	return execCtx, nil
}

func (runner *Runner[T]) NewTurn(
	execCtx *ExecutionContext, task *Task,
) (value T, result *ExecutionResult, err error) {
	attempt, result, err := execCtx.beginAttempt(runner, false)
	if attempt == nil {
		return value, result, err
	}
	changed := false
	defer func() {
		// Join owned work even when application code panics. Propagate the panic unchanged,
		// but do not allow another attempt to accept its incomplete provisional state.
		failure := recover()
		execCtx.endAttempt(attempt, failure != nil || changed && err != nil)
		if failure != nil {
			panic(failure)
		}
	}()
	owned, err := prepareTask(execCtx.Codecs(), task)
	if err != nil {
		return value, nil, fmt.Errorf("gent: validate NewTurn input for run %q: %w", execCtx.RunId(), err)
	}
	if err = execCtx.startDelivery(attempt); err != nil {
		return value, nil, err
	}
	changed = true
	if execCtx.turnOpen {
		if execCtx.Phase() == PhasePaused {
			execCtx.setPhase(execCtx.resumePhase)
		}
		if err = runner.prepareRecovery(execCtx); err != nil {
			if attemptCancellation(execCtx, err) {
				result, err = runner.controlBoundary(execCtx)
				return value, result, err
			}
			return value, nil, err
		}
	}
	if err = execCtx.admitTurn(owned); err != nil {
		return value, nil, err
	}
	return runner.execute(execCtx)
}

func (runner *Runner[T]) Continue(execCtx *ExecutionContext) (
	value T, result *ExecutionResult, err error,
) {
	attempt, result, err := execCtx.beginAttempt(runner, true)
	if attempt == nil {
		return value, result, err
	}
	defer func() {
		failure := recover()
		execCtx.endAttempt(attempt, failure != nil || err != nil)
		if failure != nil {
			panic(failure)
		}
	}()
	if err = execCtx.startDelivery(attempt); err != nil {
		return value, nil, err
	}
	if execCtx.Phase() == PhasePaused {
		execCtx.setPhase(execCtx.resumePhase)
	}
	execCtx.checkLimits()
	if execCtx.iteration <= execCtx.completedIteration {
		execCtx.EnforceIterationLimit()
	}
	if result, err = runner.controlBoundary(execCtx); result != nil || err != nil {
		return value, result, err
	}
	if err = runner.prepareRecovery(execCtx); err != nil {
		if attemptCancellation(execCtx, err) {
			result, err = runner.controlBoundary(execCtx)
			return value, result, err
		}
		return value, nil, err
	}
	return runner.execute(execCtx)
}

func (runner *Runner[T]) GetLastTurnResult(execCtx *ExecutionContext) (
	value T, result *ExecutionResult, err error,
) {
	execCtx.run.mu.RLock()
	outcome := execCtx.run.view.replay.data.LastOutcome
	execCtx.run.mu.RUnlock()
	if outcome == nil {
		return value, nil, nil
	}
	value, err = runner.outcomeValue(execCtx.Codecs(), outcome)
	if err != nil {
		return value, nil, err
	}
	return value, outcome.Result, nil
}

func (runner *Runner[T]) outcomeValue(codecs *CodecRegistry, outcome *SavedOutcome) (T, error) {
	var zero T
	if outcome.Result.TerminationReason != TerminationSuccess {
		return zero, nil
	}
	decoded, err := codecs.Decode(*outcome.Value)
	if err != nil {
		return zero, fmt.Errorf("gent: read accepted output for turn %q: %w", outcome.Result.TurnId, err)
	}
	if decoded == nil && reflect.TypeFor[T]().Kind() == reflect.Interface {
		return zero, nil
	}
	value, ok := decoded.(T)
	if !ok {
		return zero, fmt.Errorf("gent: saved output for turn %q is %T; Runner requires %v",
			outcome.Result.TurnId, decoded, reflect.TypeFor[T]())
	}
	return value, nil
}

func (runner *Runner[T]) execute(execCtx *ExecutionContext) (T, *ExecutionResult, error) {
	var zero T
	execCtx.checkLimits()
	if execCtx.iteration <= execCtx.completedIteration {
		execCtx.EnforceIterationLimit()
	}
	for {
		if result, err := runner.controlBoundary(execCtx); result != nil || err != nil {
			return zero, result, err
		}
		phase := execCtx.Phase()
		resume := phase == PhaseIterationReady || phase == PhaseDecisionReady ||
			phase == PhaseToolReady || phase == PhaseToolCompleted
		if !resume {
			if err := execCtx.ExpireRecords(execCtx.Iteration() + 1); err != nil {
				return zero, nil, err
			}
			if phase != PhaseCompactionCompleted {
				if err := runner.compact(execCtx); err != nil {
					if attemptCancellation(execCtx, err) {
						result, err := runner.controlBoundary(execCtx)
						return zero, result, err
					}
					return zero, nil, err
				}
			}
			if result, err := runner.controlBoundary(execCtx); result != nil || err != nil {
				return zero, result, err
			}
			execCtx.IncrementIteration()
			execCtx.PublishBeforeIteration()
			if err := execCtx.CommitCheckpoint(PhaseIterationReady); err != nil {
				return zero, nil, err
			}
		}
		started := time.Now()
		step, err := runner.loop.Next(execCtx)
		if err != nil && !attemptCancellation(execCtx, err) {
			return zero, nil, fmt.Errorf("gent: Loop.Next %T failed in run %q turn %q iteration %d: %w",
				runner.loop, execCtx.RunId(), execCtx.TurnId(), execCtx.Iteration(), err)
		}
		if result, err := runner.controlBoundary(execCtx); result != nil || err != nil {
			return zero, result, err
		}
		if step == nil {
			return zero, nil, fmt.Errorf("gent: Loop.Next %T returned nil in run %q turn %q",
				runner.loop, execCtx.RunId(), execCtx.TurnId())
		}
		execCtx.PublishAfterIteration(step, time.Since(started))
		// A pause after Next must resume at the next iteration, not enter the completed call
		// again with an already consumed allowance. Save this position with the pause itself.
		execCtx.mu.Lock()
		execCtx.completedIteration = execCtx.iteration
		execCtx.phase = PhaseIterationCompleted
		execCtx.mu.Unlock()
		execCtx.EnforceIterationLimit()
		if result, err := runner.controlBoundary(execCtx); result != nil || err != nil {
			return zero, result, err
		}
		if step.Action == LATerminate {
			return runner.finish(execCtx, step)
		}
		if step.Action != LAContinue {
			return zero, nil, fmt.Errorf("gent: Loop.Next returned unknown action %q", step.Action)
		}
		if err := execCtx.CommitCheckpoint(PhaseIterationCompleted); err != nil {
			return zero, nil, err
		}
	}
}

func (runner *Runner[T]) finish(execCtx *ExecutionContext, step *AgentLoopResult) (
	T, *ExecutionResult, error,
) {
	var zero T
	switch step.TerminationReason {
	case TerminationSuccess:
		if step.Value == nil || step.Refusal != nil {
			return zero, nil, errors.New("gent: success requires a typed value and no refusal")
		}
		if err := ValidateContentParts(step.Result); err != nil {
			return zero, nil, fmt.Errorf("gent: invalid accepted output content: %w", err)
		}
		execCtx.SetAcceptedValue(step.Value)
		execCtx.SetFinalContentSource(step.ContentSource)
		execCtx.SetTermination(TerminationSuccess, step.Result, nil)
	case TerminationRefused:
		if step.Refusal == nil || step.Value != nil || len(step.Result) != 0 {
			return zero, nil, errors.New("gent: refusal requires details and no accepted output")
		}
		execCtx.SetRefusal(step.Refusal)
	default:
		return zero, nil, fmt.Errorf("gent: invalid terminal loop reason %q", step.TerminationReason)
	}
	outcome := &SavedOutcome{Value: execCtx.acceptedValue, Result: execCtx.result}
	value, err := runner.outcomeValue(execCtx.Codecs(), outcome)
	if err != nil {
		return zero, nil, err
	}
	execCtx.joinProducers()
	if err := execCtx.RecordingError(); err != nil {
		return zero, nil, err
	}
	execCtx.mu.Lock()
	execCtx.turnOpen = false
	execCtx.mu.Unlock()
	execCtx.PublishAfterExecution(step.TerminationReason, nil)
	if err := execCtx.CommitCheckpoint(PhaseTerminal); err != nil {
		return zero, nil, err
	}
	return value, execCtx.run.view.replay.data.LastOutcome.Result, nil
}

func (runner *Runner[T]) controlBoundary(execCtx *ExecutionContext) (*ExecutionResult, error) {
	if err := execCtx.RecordingError(); err != nil {
		return nil, err
	}
	execCtx.mu.RLock()
	attempt := execCtx.attempt
	stopped, released := attempt.stopped, execCtx.released
	limit := execCtx.limitDetails
	execCtx.mu.RUnlock()
	if released || execCtx.ownerCtx.Err() != nil {
		return nil, fmt.Errorf("gent: run %q turn %q worker interrupted: %w", execCtx.RunId(),
			execCtx.TurnId(), errors.Join(execCtx.Context().Err(), context.Cause(execCtx.Context())))
	}
	if !stopped && limit == nil {
		return nil, nil
	}
	execCtx.joinProducers()
	if err := execCtx.RecordingError(); err != nil {
		return nil, err
	}
	reason := TerminationStopped
	if limit != nil {
		reason = TerminationLimitExceeded
	}
	execCtx.mu.Lock()
	execCtx.resumePhase = execCtx.phase
	execCtx.mu.Unlock()
	execCtx.SetTermination(reason, nil, nil)
	if err := execCtx.recordAttemptEvent("execution_paused", execCtx.Result()); err != nil {
		return nil, err
	}
	if err := execCtx.CommitCheckpoint(PhasePaused); err != nil {
		return nil, err
	}
	return execCtx.run.view.Checkpoint.Outcome.Result, nil
}

func attemptCancellation(execCtx *ExecutionContext, err error) bool {
	ctx := execCtx.Context()
	return onlyCancellation(err, ctx.Err(), context.Cause(ctx))
}
