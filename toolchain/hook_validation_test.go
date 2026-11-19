package toolchain

import (
	"context"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/schema"
	"github.com/stretchr/testify/assert"
)

// Hook defects must preserve the schema diagnostics without accepting a tool-ready save or
// sending them as a model correction. The normal model-input path keeps its existing assertions.
func runNativeTestCallWithHookError(t *testing.T, chain gent.ToolChain, config *gent.Config,
	call *gent.ModelToolCallRequest, details string,
) (*gent.ToolCallRecord, *gent.ExecutionContext) {
	t.Helper()
	if details == "" {
		return runNativeTestCall(t, chain, config, call)
	}
	config.ToolChain = chain
	var plan *gent.ToolPlan
	loop := planTestLoop(chain, []*gent.ModelToolCallRequest{call}, &plan)
	runner, execCtx, store := newToolTestRun(t, context.Background(), loop, config)
	value, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Run the tool.")))
	assert.Equal(t, "", value)
	assert.Nil(t, result)
	var validation *schema.ValidationError
	assert.ErrorAs(t, err, &validation)
	assert.Equal(t, details, schema.SafeValidationDetails(err))
	var phases []gent.ExecutionPhase
	for _, data := range store.batches {
		var batch gent.SaveBatch
		if !assert.NoError(t, store.codecs.Unmarshal(data, &batch)) {
			return nil, execCtx
		}
		phases = append(phases, batch.Checkpoint.Phase)
	}
	assert.Equal(t, []gent.ExecutionPhase{gent.PhaseInitial, gent.PhaseIterationReady}, phases)
	assert.Nil(t, plan.ReadyCall)
	return nil, execCtx
}

func TestInvalidHookInputDoesNotAcceptToolPreparation(t *testing.T) {
	type input struct{ replacement any }
	type expected struct {
		calls  int
		phases []gent.ExecutionPhase
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	want := expected{phases: []gent.ExecutionPhase{gent.PhaseInitial, gent.PhaseIterationReady}}
	cases := []testCase{
		{"schema violation", input{validationInput{}}, want},
		{"wrong Go type", input{map[string]any{"id": "O1"}}, want},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			tool := gent.DefineTool("lookup", func(_ context.Context, value validationInput) (
				*gent.ToolExecutionResponse, error,
			) {
				calls++
				assert.Equal(t, validationInput{ID: "O1"}, value)
				return capturedToolResponse("Found", nil)
			}).ParameterSchema(map[string]any{"type": "object", "properties": map[string]any{
				"id": map[string]any{"type": "string", "minLength": 1}},
				"required": []string{"id"}, "additionalProperties": false}).MustBuild()
			chain := New().RegisterTool(tool)
			var plan *gent.ToolPlan
			loop := planTestLoop(chain, []*gent.ModelToolCallRequest{
				nativeCall("lookup", `{"id":"O1"}`)}, &plan)
			config := &gent.Config{ToolChain: chain, Hooks: replacementInputHooks(test.input.replacement)}
			runner, execCtx, store := newToolTestRun(t, context.Background(), loop, config)
			value, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Read O1.")))
			assert.Error(t, err)
			assert.Equal(t, "", value)
			assert.Nil(t, result)
			actual := expected{calls: calls}
			state := gent.NewRunState(execCtx.RunId())
			for _, data := range store.batches {
				var batch gent.SaveBatch
				if !assert.NoError(t, store.codecs.Unmarshal(data, &batch)) {
					return
				}
				actual.phases = append(actual.phases, batch.Checkpoint.Phase)
				applied, err := state.Apply(&batch, store.codecs)
				if !assert.NoError(t, err) || !assert.Equal(t, gent.ApplyApplied, applied.Status) {
					return
				}
			}
			assert.Equal(t, test.expected, actual)
			full, err := store.codecs.MarshalRunState(state)
			assert.NoError(t, err)
			assert.Equal(t, store.runStateSnapshot, full)
			// Fix the Hook and restore accepted input. The invalid proposal never reached Call.
			fixedRunner, fixedCtx := restoreToolTest(t, loop,
				&gent.Config{ToolChain: chain, Codecs: store.codecs}, full)
			value, result, err = fixedRunner.Continue(fixedCtx)
			assert.NoError(t, err)
			assert.Equal(t, "Done", value)
			assert.Equal(t, &gent.ExecutionResult{Status: gent.ExecutionFinished,
				TurnId: execCtx.TurnId(), TerminationReason: gent.TerminationSuccess,
				Output: []gent.ContentPart{gent.TextPart("Done")}}, result)
			assert.Equal(t, 1, calls)
		})
	}
}
