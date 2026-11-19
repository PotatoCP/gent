package toolchain

import (
	"context"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/stretchr/testify/assert"
)

// Preparation saves a plan without effects. A malformed argument value is a correlated
// InvalidArguments reply during execution, so the model can repair the selected call.
func TestNativePreparationDoesNotInvokeTools(t *testing.T) {
	calls := 0
	tool := gent.DefineTool("read", func(context.Context, struct{}) (
		*gent.ToolExecutionResponse, error,
	) {
		calls++
		return capturedToolResponse("read", nil)
	}).MustBuild()
	chain := New().RegisterTool(tool)
	runToolScenario(t, chain, &gent.Config{}, func(execCtx *gent.ExecutionContext) {
		plan, err := chain.PrepareCalls(execCtx, []*gent.ModelToolCallRequest{nativeCall("read", "{"),
			nativeCall("read", "{}")})
		assert.NoError(t, err)
		assert.Equal(t, 0, calls)
		first, err := chain.ExecuteNext(execCtx, plan, testFormat())
		assert.NoError(t, err)
		assert.Equal(t, gent.ToolInvalidArguments, first.Execution().Status)
		second, err := chain.ExecuteNext(execCtx, plan, testFormat())
		assert.NoError(t, err)
		assert.Equal(t, gent.ToolExecuted, second.Execution().Status)
		assert.Equal(t, 1, calls)
		_, err = chain.ExecuteNext(execCtx, plan, testFormat())
		assert.ErrorIs(t, err, gent.ErrToolPlanComplete)
	})
}

func TestNativeCallIdentitySurvivesSharedExecution(t *testing.T) {
	chain := NewSearch(SearchHintSimpleList).RegisterEngine(NewRegexToolSearcher())
	chain.RegisterTool(gent.DefineTool("read", func(context.Context, struct{}) (
		*gent.ToolExecutionResponse, error,
	) {
		return capturedToolResponse("saved", nil)
	}).MustBuild())
	assert.NoError(t, chain.Initialize())
	request := nativeCall(executeToolName, `{"tool":"read","args":{}}`)
	request.ID = "native-123"
	result, _ := runNativeTestCall(t, chain, &gent.Config{}, request)
	assert.Equal(t, "read", result.Name())
	assert.Equal(t, "execute_tool", result.Response().Name())
	assert.Equal(t, "native-123", result.Response().ToolCallID())
	assert.NotEqual(t, result.ToolCallID(), result.Response().ToolCallID())
	assert.Equal(t, []gent.ContentPart{result.Response()}, result.ModelMessage().Parts)
	assert.Equal(t, gent.ChatMessageTypeTool, result.ModelMessage().Role)
}
