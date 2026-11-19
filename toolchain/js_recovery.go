package toolchain

import (
	"errors"
	"fmt"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/toolchain/jsruntime"
)

// A program is a sequence of native batches. Save each batch before it can start an effect.
// The outer plan remains under gent:tool_plan; inner checkpoints must never replace it.
type programState struct {
	AttemptId string
	CallId    string
	Script    string
	Completed []*gent.ToolCallRecord
}

// Save keys instead of shared plan pointers. Each worker can then save its own cursor and
// ready input without reading a plan that another worker is changing.
type parallelProgramCalls struct {
	Keys []string
}

func loadProgram(execCtx *gent.ExecutionContext) (*programState, bool, error) {
	value, found, err := execCtx.CheckpointState("gent:program")
	if err != nil || !found {
		return nil, found, err
	}
	state, ok := value.(*programState)
	if !ok {
		return nil, false, errors.New("invalid JavaScript recovery state")
	}
	return state, true, nil
}

func (w *JsToolChainWrapper) executeProgramCalls(execCtx *gent.ExecutionContext,
	calls []*gent.ModelToolCallRequest, format gent.TextFormat,
) ([]*gent.ToolCallRecord, error) {
	if err := execCtx.RecordingError(); err != nil {
		return nil, err
	}
	if err := execCtx.Context().Err(); err != nil {
		return nil, err
	}
	program, found, err := loadProgram(execCtx)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, errors.New("JavaScript call has no active program")
	}
	if program.AttemptId != execCtx.AttemptId() {
		return nil, errors.New("JavaScript attempt changed")
	}
	if len(calls) > 1 {
		return w.executeParallelProgramCalls(execCtx, program, calls, format)
	}
	plan, err := w.wrapped.PrepareCalls(execCtx, calls)
	if err != nil {
		return nil, fmt.Errorf("gent: run_code ToolChain.PrepareCalls for tool %q: %w",
			calls[0].FunctionCall.Name, err)
	}
	plan.CheckpointKey = "gent:program_calls"
	if err := execCtx.SetToolPlan(plan); err != nil {
		return nil, err
	}
	if err := execCtx.RecordingError(); err != nil {
		return nil, err
	}
	for plan.NextCall < len(plan.Calls) {
		call := plan.Calls[plan.NextCall]
		if _, err := w.wrapped.ExecuteNext(execCtx, plan, format); err != nil {
			return nil, fmt.Errorf("gent: run_code ToolChain.ExecuteNext for tool %q call %q: %w",
				call.Name, call.Id, err)
		}
	}
	program.Completed = append(program.Completed, plan.Results...)
	if err := execCtx.SetCheckpointState("gent:program", program); err != nil {
		return nil, err
	}
	execCtx.ClearCheckpointState("gent:program_calls")
	if err := execCtx.CommitCheckpoint(gent.PhaseToolCompleted); err != nil {
		return nil, err
	}
	return plan.Results, nil
}

func (w *JsToolChainWrapper) executeParallelProgramCalls(execCtx *gent.ExecutionContext,
	program *programState, calls []*gent.ModelToolCallRequest, format gent.TextFormat,
) ([]*gent.ToolCallRecord, error) {
	chain, ok := w.wrapped.(gent.ParallelToolChain)
	if !ok || !chain.SupportsParallelCalls() {
		return nil, errors.New("tool.parallelCall requires a ParallelToolChain")
	}
	batch := &parallelProgramCalls{}
	plans := make([]*gent.ToolPlan, 0, len(calls))
	for _, call := range calls {
		plan, err := chain.PrepareCalls(execCtx, []*gent.ModelToolCallRequest{call})
		if err != nil {
			return nil, fmt.Errorf("gent: run_code ToolChain.PrepareCalls for tool %q: %w",
				call.FunctionCall.Name, err)
		}
		if err := checkToolPlan(execCtx, plan); err != nil {
			return nil, err
		}
		plan.CheckpointKey = "gent:program_call:" + plan.Calls[0].Id
		plans = append(plans, plan)
		batch.Keys = append(batch.Keys, plan.CheckpointKey)
	}
	for _, plan := range plans {
		if err := execCtx.SetToolPlan(plan); err != nil {
			return nil, err
		}
	}
	if err := execCtx.SetCheckpointState("gent:program_calls", batch); err != nil {
		return nil, err
	}
	if err := execCtx.RecordingError(); err != nil {
		return nil, err
	}
	// Keep the supplied chain as the receiver. A promoted base method must not bypass an
	// ExecuteNext override that checks whether a tool call is allowed.
	result, err := executeParallelToolPlans(execCtx, chain, plans, format)
	if err != nil {
		return nil, err
	}
	program.Completed = append(program.Completed, result...)
	if err := execCtx.SetCheckpointState("gent:program", program); err != nil {
		return nil, err
	}
	clearParallelProgramCalls(execCtx, batch)
	if err := execCtx.CommitCheckpoint(gent.PhaseToolCompleted); err != nil {
		return nil, err
	}
	return result, nil
}

func clearParallelProgramCalls(execCtx *gent.ExecutionContext, batch *parallelProgramCalls) {
	for _, key := range batch.Keys {
		execCtx.ClearCheckpointState(key)
	}
	execCtx.ClearCheckpointState("gent:program_calls")
}

// An accepted outer reply closes the program even when its last control checkpoint is older.
// Remove saved inner plans without recovering their already accepted effects again.
func clearProgram(execCtx *gent.ExecutionContext) error {
	value, _, err := execCtx.CheckpointState("gent:program_calls")
	if err != nil {
		return err
	}
	if batch, ok := value.(*parallelProgramCalls); ok {
		clearParallelProgramCalls(execCtx, batch)
	}
	execCtx.ClearCheckpointState("gent:program", "gent:program_calls")
	return nil
}

func (w *JsToolChainWrapper) recoverProgram(execCtx *gent.ExecutionContext,
	outer *gent.ToolPlan, program *programState, format gent.TextFormat,
) (*gent.ToolCallRecord, error) {
	call := outer.Calls[outer.NextCall]
	if program.CallId != call.Id {
		return nil, errors.New("JavaScript recovery call does not match")
	}
	results := append([]*gent.ToolCallRecord(nil), program.Completed...)
	value, found, err := execCtx.CheckpointState("gent:program_calls")
	if err != nil {
		return nil, err
	}
	var parallel *parallelProgramCalls
	if found {
		var plans []*gent.ToolPlan
		switch saved := value.(type) {
		case *gent.ToolPlan:
			plans = append(plans, saved)
		case *parallelProgramCalls:
			parallel = saved
			for _, key := range saved.Keys {
				value, found, err := execCtx.CheckpointState(key)
				if err != nil {
					return nil, err
				}
				plan, ok := value.(*gent.ToolPlan)
				if !found || !ok {
					return nil, errors.New("invalid parallel JavaScript call plan")
				}
				plans = append(plans, plan)
			}
		default:
			return nil, errors.New("invalid JavaScript call plan")
		}
		for _, inner := range plans {
			partial, err := w.recoverProgramCalls(execCtx, inner, format)
			if err != nil {
				return nil, err
			}
			results = append(results, partial...)
		}
	}
	report := &gent.ToolRecoveryResponse{Status: gent.ToolRecoveryUnresolved,
		Parts: jsruntime.ProgramParts(results, execCtx.SteeringMessages(), nil),
		Instructions: fmt.Sprintf(`JavaScript execution was interrupted. The script was not restarted.
Script:
%s
Review the results and make a new plan.`, program.Script)}
	snapshot, err := execCtx.SnapshotDiagnostic(report)
	if err != nil {
		return nil, err
	}
	result, err := gent.NewToolCallRecordBuilder(call.Id, execCtx.AttemptId(), call.Name).
		Arguments(call.RawArguments, call.Args).Recovery(report).ReturnedRecovery(snapshot).
		ModelReply(call.ModelCallID, call.ModelName,
			gent.PrepareRecoveryParts(report, execCtx.SteeringMessages())...).Build()
	if err != nil {
		return nil, err
	}
	execCtx.PublishAfterToolCall(call.Name, nil, result, 0, nil,
		gent.WithToolCallId(call.Id), gent.WithToolCallAttempt(program.AttemptId))
	if err := execCtx.RecordingError(); err != nil {
		return nil, err
	}
	if parallel != nil {
		clearParallelProgramCalls(execCtx, parallel)
	}
	execCtx.ClearCheckpointState("gent:program", "gent:program_calls")
	return completePlannedTool(execCtx, outer, result)
}

// Recovery observes each unfinished inner call. No script or business call is replayed.
func (w *JsToolChainWrapper) recoverProgramCalls(execCtx *gent.ExecutionContext,
	plan *gent.ToolPlan, format gent.TextFormat,
) ([]*gent.ToolCallRecord, error) {
	for plan.NextCall < len(plan.Calls) {
		if _, err := w.wrapped.ExecuteNext(execCtx, plan, format); err != nil {
			return nil, err
		}
	}
	return plan.Results, nil
}
