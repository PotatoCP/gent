package gent

import (
	"context"
	"errors"
	"reflect"

	"github.com/rickchristie/gent/schema"
)

var ErrToolPlanComplete = errors.New("gent: tool plan is complete")

type PlannedToolCall struct {
	ModelCallID  string
	ModelName    string
	RawArguments string
	Id           string
	Name         string
	Args         map[string]any
}

type SavedToolCall struct {
	Id        string
	Name      string
	Args      map[string]any
	Input     EncodedValue
	AttemptId string
}

type ToolPlan struct {
	// CheckpointKey separates an inner program plan from its outer native call.
	CheckpointKey string
	AttemptId     string
	Version       int
	Calls         []PlannedToolCall
	NextCall      int
	ReadyCall     *SavedToolCall
	Results       []*ToolCallRecord
	State         EncodedValue
}

// NewToolCallId allocates a new logical tool identity. It fails before sequence exhaustion can
// reuse an accepted identity. A recovered operation must keep its saved ID instead of calling this.
func (execCtx *ExecutionContext) NewToolCallId() (string, error) { return execCtx.nextToolCallId() }

func (execCtx *ExecutionContext) SetToolPlan(plan *ToolPlan) error {
	key := plan.CheckpointKey
	if key == "" {
		key = "gent:tool_plan"
	}
	return execCtx.SetCheckpointState(key, plan)
}

func (execCtx *ExecutionContext) SavedToolPlan() (*ToolPlan, bool, error) {
	value, ok, err := execCtx.CheckpointState("gent:tool_plan")
	if err != nil || !ok {
		return nil, ok, err
	}
	plan, ok := value.(*ToolPlan)
	if !ok || plan == nil || plan.Version != 1 || plan.NextCall < 0 || plan.NextCall > len(
		plan.Calls,
	) {
		return nil, false, errors.New("gent: invalid saved tool plan")
	}
	if len(plan.Results) != plan.NextCall {
		return nil, false, errors.New("gent: tool plan results do not match its cursor")
	}
	seen := map[string]bool{}
	for _, call := range plan.Calls {
		if call.Id == "" || call.Name == "" || seen[call.Id] {
			return nil, false, errors.New("gent: invalid planned tool identity")
		}
		seen[call.Id] = true
	}
	if ready := plan.ReadyCall; ready != nil {
		if plan.NextCall >= len(plan.Calls) || ready.Id != plan.Calls[plan.NextCall].Id ||
			ready.Name != plan.Calls[plan.NextCall].Name || ready.AttemptId == "" {
			return nil, false, errors.New("gent: ready tool does not match planned call")
		}
	}
	return plan, true, nil
}

// SelectToolCall selects the default plan for sequential calls. Concurrent calls must use
// BindToolCall and pass their logical ID to PublishBeforeToolCall.
func (execCtx *ExecutionContext) SelectToolCall(plan *ToolPlan) {
	execCtx.run.mu.Lock()
	defer execCtx.run.mu.Unlock()
	execCtx.currentToolPlan = plan
}

// BindToolCall associates an explicit call ID with its plan until the returned function runs.
// Each concurrent call must own a separate plan and checkpoint key.
func (execCtx *ExecutionContext) BindToolCall(plan *ToolPlan) func() {
	id := plan.Calls[plan.NextCall].Id
	execCtx.run.mu.Lock()
	if execCtx.boundToolPlans == nil {
		execCtx.boundToolPlans = make(map[string]*ToolPlan)
	}
	execCtx.boundToolPlans[id] = plan
	execCtx.run.mu.Unlock()
	return func() {
		execCtx.run.mu.Lock()
		delete(execCtx.boundToolPlans, id)
		execCtx.run.mu.Unlock()
	}
}

func (execCtx *ExecutionContext) toolPlanFor(id string) *ToolPlan {
	execCtx.run.mu.RLock()
	defer execCtx.run.mu.RUnlock()
	if plan := execCtx.boundToolPlans[id]; plan != nil {
		return plan
	}
	plan := execCtx.currentToolPlan
	if plan != nil && plan.NextCall < len(plan.Calls) && plan.Calls[plan.NextCall].Id == id {
		return plan
	}
	return nil
}

func (execCtx *ExecutionContext) selectedToolCallId() string {
	execCtx.run.mu.RLock()
	defer execCtx.run.mu.RUnlock()
	plan := execCtx.currentToolPlan
	if plan == nil || plan.NextCall >= len(plan.Calls) {
		return ""
	}
	return plan.Calls[plan.NextCall].Id
}

func (execCtx *ExecutionContext) saveToolReady(event *BeforeToolCallEvent) error {
	plan := execCtx.toolPlanFor(event.ToolCallId)
	if plan == nil {
		return execCtx.CommitCheckpoint(PhaseToolReady)
	}
	input, err := execCtx.Codecs().Encode(event.Args)
	if err != nil {
		return execCtx.stopRecording(err)
	}
	call := plan.Calls[plan.NextCall]
	plan.ReadyCall = &SavedToolCall{Id: call.Id, Name: call.Name, Args: call.Args,
		Input: input, AttemptId: execCtx.AttemptId()}
	if err := execCtx.SetToolPlan(plan); err != nil {
		return execCtx.stopRecording(err)
	}
	return execCtx.CommitCheckpoint(PhaseToolReady)
}

func (execCtx *ExecutionContext) ToolContext(id string) context.Context {
	return context.WithValue(execCtx.Context(), toolIdentityKey{}, ToolIdentity{RunId: execCtx.RunId(),
		ContextId: execCtx.ContextId(), ToolCallId: id, AttemptId: execCtx.AttemptId()})
}

// RecoverTool reuses accepted evidence or reports on an interrupted plan. It never executes
// a pending call. Even a call that never reached dispatch returns control to the model.
func (execCtx *ExecutionContext) RecoverTool(plan *ToolPlan, recover ToolRecoveryFunc[EncodedValue],
	expectedSchema *schema.Schema,
) (
	*ToolCallRecord, bool, error,
) {
	if plan.AttemptId == execCtx.AttemptId() {
		return nil, false, nil
	}
	call := plan.Calls[plan.NextCall]
	if result, found, err := execCtx.SavedToolCallRecord(call.Id); err != nil || found {
		return result, found, err
	}
	attempt := plan.AttemptId
	response := &ToolRecoveryResponse{Status: ToolRecoveryNotStarted}
	var diagnostic error
	var input *EncodedValue
	if ready := plan.ReadyCall; ready != nil {
		attempt, input = ready.AttemptId, &ready.Input
		request := NewToolRecoveryRequest(
			ToolIdentity{
				RunId:      execCtx.RunId(),
				ContextId:  execCtx.ContextId(),
				ToolCallId: ready.Id,
				AttemptId:  ready.AttemptId,
			},
			ready.Name,
			ready.Args,
			ready.Input,
		)
		recoveryContext := context.WithValue(execCtx.Context(), recoveryCodecsKey{}, execCtx.Codecs())
		response, diagnostic = recover(recoveryContext, request)
	}
	if execCtx.Context().Err() != nil {
		return nil, false, execCtx.Context().Err()
	}
	returned, err := execCtx.SnapshotDiagnostic(response)
	if err != nil {
		return nil, false, err
	}
	if diagnostic == nil {
		diagnostic = ValidateToolRecovery(response)
		if diagnostic == nil && response.Execution != nil {
			accepted := *response
			execution := *response.Execution
			if execution.Status != ToolExecuted {
				execution.Parts = nil
			}
			if execution.Status == ToolInvalidArguments {
				execution.Instructions = expectedSchema.Correction(call.Name, execution.Instructions)
			}
			accepted.Execution = &execution
			response = &accepted
		}
	}
	savedError, err := execCtx.SnapshotDiagnostic(diagnostic)
	if err != nil {
		return nil, false, err
	}
	builder := NewToolCallRecordBuilder(call.Id, attempt, call.Name).
		Arguments(call.RawArguments, call.Args).Input(input).ReturnedRecovery(returned).Error(savedError)
	var parts []ContentPart
	if diagnostic != nil {
		builder.RecoveryError()
		parts = []ContentPart{TextPart(execCtx.SteeringMessages().RecoveryError)}
	} else {
		builder.Recovery(response)
		parts = PrepareRecoveryParts(response, execCtx.SteeringMessages())
	}
	result, err := builder.ModelReply(call.ModelCallID, call.ModelName, parts...).Build()
	if err != nil {
		return nil, false, err
	}
	execCtx.PublishAfterToolCall(call.Name, input, result, 0, diagnostic,
		WithToolCallId(call.Id), WithToolCallAttempt(attempt))
	return result, true, execCtx.RecordingError()
}

// SavedToolCallRecord reads the last accepted record for a logical tool-call ID. Check it before
// recovery or repeated custom-loop work so
// the prepared reply, diagnostics, and accounting remain unchanged. Pending events are excluded.
// Reading a record does not publish events or update counters. The returned record is read-only.
func (execCtx *ExecutionContext) SavedToolCallRecord(id string) (*ToolCallRecord, bool, error) {
	var saved *RecordedEvent
	execCtx.run.mu.RLock()
	for i := len(execCtx.run.view.Activity) - 1; i >= 0; i-- {
		event := execCtx.run.view.Activity[i]
		if event.ToolCallId == id && event.Type == "tool_call_finished" {
			saved = event
			break
		}
	}
	execCtx.run.mu.RUnlock()
	if saved == nil {
		return nil, false, nil
	}
	// Accepted event payloads are immutable. Decode only the selected record, not the whole run.
	value, err := execCtx.DecodeEvent(saved)
	if err != nil {
		return nil, false, err
	}
	completed, ok := value.(*AfterToolCallEvent)
	if !ok {
		return nil, false, errors.New("gent: saved tool event has an invalid payload")
	}
	result, ok := completed.Output.(*ToolCallRecord)
	if !ok || result == nil {
		return nil, false, errors.New("gent: saved tool event has no call record")
	}
	return result, true, nil
}

// SnapshotDiagnostic captures optional private evidence. Encoded snapshots are retained without
// decoding their old Go types. Ordinary errors use the registered error codec.
func (execCtx *ExecutionContext) SnapshotDiagnostic(value any) (*EncodedValue, error) {
	if value == nil {
		return nil, nil
	}
	v := reflect.ValueOf(value)
	if v.Kind() == reflect.Pointer && v.IsNil() {
		return nil, nil
	}
	encoded, err := execCtx.Codecs().Encode(value)
	if err != nil {
		return nil, err
	}
	if encoded.Type == "nil" {
		return nil, nil
	}
	return &encoded, nil
}
