package react

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/rickchristie/gent"
	agentutil "github.com/rickchristie/gent/agents"
	core "github.com/rickchristie/gent/internal/core"
)

type iterationFrame struct {
	Iteration           int
	Response            *gent.ModelCallResponse
	Repetition          *agentutil.RepetitionResult
	ToolPlanActive      bool
	ToolResultsPrepared bool
}

func (r *Agent[T]) Next(execCtx *gent.ExecutionContext) (*gent.AgentLoopResult, error) {
	result, err := r.next(execCtx)
	if err == nil {
		execCtx.ClearCheckpointState("gent:react_frame", "gent:tool_plan", "gent:search_seen")
	}
	return result, err
}

func (r *Agent[T]) RegisterCodecs(registry *gent.CodecRegistry) error {
	if err := registry.RegisterValueType(reflect.TypeFor[T]()); err != nil {
		return err
	}
	if err := registry.RegisterValueType(reflect.TypeOf(iterationFrame{})); err != nil {
		return err
	}
	return nil
}

func (r *Agent[T]) ConfigureExecution(execCtx *gent.ExecutionContext) error {
	core.BindManagedRecovery(execCtx, r.prepareRecovery)
	return r.RegisterCodecs(execCtx.Codecs())
}

// prepareRecovery builds the complete native call/reply group before Gent accepts After Recovery.
// The saved frame prevents a later Next call from appending those prepared messages a second time.
func (r *Agent[T]) prepareRecovery(execCtx *gent.ExecutionContext) (bool, error) {
	frame, err := r.loadFrame(execCtx)
	if err != nil || !frame.ToolPlanActive || frame.ToolResultsPrepared {
		return false, err
	}
	plan, found, err := execCtx.SavedToolPlan()
	if err != nil {
		return false, err
	}
	if !found || frame.Response == nil || len(frame.Response.Choices) == 0 {
		return false, errors.New("gent: saved ReAct tool frame has no plan or model choice")
	}
	for plan.NextCall < len(plan.Calls) {
		if _, err := r.tools(execCtx).ExecuteNext(execCtx, plan, r.format); err != nil {
			return false, err
		}
	}
	if err := execCtx.AppendRecord(
		nativeToolEntry(
			frame.Response.Choices[0],
			plan.Results,
		),
		true,
	); err != nil {
		return false, err
	}
	frame.ToolResultsPrepared = true
	if err := execCtx.SetCheckpointState("gent:react_frame", frame); err != nil {
		return false, err
	}
	return true, nil
}

func (r *Agent[T]) loadFrame(execCtx *gent.ExecutionContext) (*iterationFrame, error) {
	if err := r.RegisterCodecs(execCtx.Codecs()); err != nil {
		return nil, err
	}
	value, ok, err := execCtx.CheckpointState("gent:react_frame")
	if err != nil {
		return nil, err
	}
	if !ok {
		return &iterationFrame{Iteration: execCtx.Iteration()}, nil
	}
	frame, ok := value.(*iterationFrame)
	if !ok || frame == nil {
		return nil, errors.New("gent: invalid ReAct frame")
	}
	if frame.Iteration != execCtx.Iteration() {
		return nil, fmt.Errorf("%w: ReAct iteration frame", gent.ErrIncompatibleCheckpoint)
	}
	return frame, nil
}

func saveFrame(execCtx *gent.ExecutionContext, frame *iterationFrame) error {
	// The next approved tool, iteration, or terminal boundary accepts this frame. A model
	// response or a locally prepared plan does not need a separate storage roundtrip.
	return execCtx.SetCheckpointState("gent:react_frame", frame)
}

// executeToolPlan keeps the saved execution path for native calls.
func (r *Agent[T]) executeToolPlan(
	execCtx *gent.ExecutionContext, frame *iterationFrame,
	prepare func() (*gent.ToolPlan, error),
	execute func(
		*gent.ExecutionContext, *gent.ToolPlan, gent.TextFormat,
	) (*gent.ToolCallRecord, error),
) ([]*gent.ToolCallRecord, error) {
	var plan *gent.ToolPlan
	var err error
	if frame.ToolPlanActive {
		var found bool
		plan, found, err = execCtx.SavedToolPlan()
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, errors.New("gent: ReAct frame has no tool plan")
		}
	} else {
		plan, err = prepare()
		if err != nil {
			return nil, fmt.Errorf("gent: ToolChain.PrepareCalls: %w", err)
		}
		frame.ToolPlanActive = true
		if err := execCtx.SetToolPlan(plan); err != nil {
			return nil, err
		}
		if err := saveFrame(execCtx, frame); err != nil {
			return nil, err
		}
	}
	for plan.NextCall < len(plan.Calls) {
		call := plan.Calls[plan.NextCall]
		if _, err := execute(execCtx, plan, r.format); err != nil {
			return nil, fmt.Errorf("gent: ToolChain.ExecuteNext for tool %q call %q: %w",
				call.Name, call.Id, err)
		}
	}
	return plan.Results, nil
}
