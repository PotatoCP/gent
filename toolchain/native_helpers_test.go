package toolchain

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rickchristie/gent"
)

// testCalls turns compact test data into native call arguments. The production registry receives
// only native calls, including in the execution tests that predate this API.
func testCalls(content string) []*gent.ModelToolCallRequest {
	if strings.TrimSpace(content) == "" {
		return nil
	}
	type call struct {
		Tool string          `json:"tool"`
		Args json.RawMessage `json:"args"`
	}
	var calls []call
	if strings.HasPrefix(strings.TrimSpace(content), "[") {
		if err := json.Unmarshal([]byte(content), &calls); err != nil {
			panic(err)
		}
	} else {
		var single call
		if err := json.Unmarshal([]byte(content), &single); err != nil {
			panic(err)
		}
		calls = append(calls, single)
	}
	result := make([]*gent.ModelToolCallRequest, len(calls))
	for i, call := range calls {
		args := string(call.Args)
		if args == "" || args == "null" {
			args = "{}"
		}
		result[i] = nativeCall(call.Tool, args)
	}
	return result
}

func executeTestCalls(chain gent.ToolChain, execCtx *gent.ExecutionContext,
	content string, format gent.TextFormat,
) ([]*gent.ToolCallRecord, error) {
	return executeToolPlan(execCtx, chain, testCalls(content), format)
}

func executeTestCode(chain *JsToolChainWrapper, execCtx *gent.ExecutionContext,
	code string, format gent.TextFormat,
) ([]*gent.ToolCallRecord, error) {
	args, err := json.Marshal(map[string]string{"code": code})
	if err != nil {
		return nil, err
	}
	calls := []*gent.ModelToolCallRequest{nativeCall("run_code", string(args))}
	result, err := executeToolPlan(execCtx, chain, calls, format)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func executeToolPlan(execCtx *gent.ExecutionContext, chain gent.ToolChain,
	calls []*gent.ModelToolCallRequest, format gent.TextFormat) ([]*gent.ToolCallRecord, error) {
	plan, err := chain.PrepareCalls(execCtx, calls)
	if err != nil {
		return nil, err
	}
	for plan.NextCall < len(plan.Calls) {
		if _, err := chain.ExecuteNext(execCtx, plan, format); err != nil {
			return nil, fmt.Errorf("execute tool %d: %w", plan.NextCall, err)
		}
	}
	return plan.Results, nil
}
