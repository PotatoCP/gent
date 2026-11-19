package toolchain

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/rickchristie/gent"
)

const nativeCodeTool = "run_code"

// NativeTools exposes the same local JavaScript runtime through a function call. Code-only mode
// still includes the available tool schemas in its description so code can use exact arguments.
func (w *JsToolChainWrapper) NativeTools(
	execCtx *gent.ExecutionContext,
) ([]*gent.ToolDefinition, error) {
	chain := w.wrapped
	tools, err := chain.NativeTools(execCtx)
	if err != nil {
		return nil, err
	}
	var catalog strings.Builder
	for i, tool := range tools {
		owned := *tool
		function := *tool.Function
		owned.Function = &function
		tool = &owned
		tools[i] = tool
		if tool.Function.Name == nativeCodeTool {
			return nil, fmt.Errorf("tool name %q is reserved for JavaScript execution", nativeCodeTool)
		}
		if w.directCallDisabled {
			encoded, err := json.Marshal(tool.Function)
			if err != nil {
				return nil, err
			}
			catalog.Write(encoded)
			catalog.WriteByte('\n')
		}
		if w.directCallGuidance != "" {
			tool.Function.Description += "\n\n" + w.directCallGuidance
		}
	}
	guidance := w.codeGuidance
	if guidance == "" {
		guidance = defaultCodeGuidance()
	}
	description := "Run JavaScript on the application server.\n" +
		"Use tool.call or tool.parallelCall for registered tools.\n\n" + guidance
	if w.integerMode == gent.PTCIntegerBigInt {
		description += "\nCaptured integers use bigint. Floats use number. " +
			"Use bigint operands for integer arithmetic. " +
			"tool.call and console.log accept bigint. Native JSON.stringify rejects bigint."
	} else {
		description += "\nCaptured integers use number. Large integers can lose precision in JavaScript."
	}
	if w.directCallDisabled {
		description += "\n\nAvailable tools inside code:\n" + catalog.String()
		tools = nil
	} else {
		description += "\n\nThe other function definitions also describe the tools available in code."
	}
	tools = append(tools, &gent.ToolDefinition{Type: "function", Function: &gent.FunctionDefinition{
		Name: nativeCodeTool, Description: description,
		Parameters: map[string]any{"type": "object", "properties": map[string]any{
			"code": map[string]any{"type": "string"},
		}, "required": []string{"code"}, "additionalProperties": false},
	}})
	return tools, nil
}

// RegisterCodecs also covers tool outputs saved inside a program result.
func (w *JsToolChainWrapper) RegisterCodecs(registry *gent.CodecRegistry) error {
	if err := registry.RegisterValueType(reflect.TypeOf(codeInput{})); err != nil {
		return err
	}
	if err := registry.RegisterValueType(reflect.TypeOf(programState{})); err != nil {
		return err
	}
	if err := registry.RegisterValueType(reflect.TypeOf(parallelProgramCalls{})); err != nil {
		return err
	}
	return w.wrapped.RegisterCodecs(registry)
}

func (w *JsToolChainWrapper) PrepareCalls(
	execCtx *gent.ExecutionContext, calls []*gent.ModelToolCallRequest,
) (*gent.ToolPlan, error) {
	if err := w.RegisterCodecs(execCtx.Codecs()); err != nil {
		return nil, err
	}
	return w.wrapped.PrepareCalls(execCtx, calls)
}

type codeInput struct {
	Code string `json:"code"`
}

func (w *JsToolChainWrapper) ExecuteNext(execCtx *gent.ExecutionContext,
	plan *gent.ToolPlan, format gent.TextFormat,
) (*gent.ToolCallRecord, error) {
	if err := checkToolPlan(execCtx, plan); err != nil {
		return nil, err
	}
	call := plan.Calls[plan.NextCall]
	if call.Name != nativeCodeTool && !w.directCallDisabled {
		return w.wrapped.ExecuteNext(execCtx, plan, format)
	}
	if plan.AttemptId != execCtx.AttemptId() {
		// Accepted outer evidence takes priority over an older program checkpoint. Rebuilding
		// the reply would lose console output and the script's explicit byte selections.
		record, found, err := execCtx.SavedToolCallRecord(call.Id)
		if err != nil {
			return nil, err
		}
		if found {
			if err := clearProgram(execCtx); err != nil {
				return nil, err
			}
			return completePlannedTool(execCtx, plan, record)
		}
	}
	program, found, err := loadProgram(execCtx)
	if err != nil {
		return nil, err
	}
	if found {
		return w.recoverProgram(execCtx, plan, program, format)
	}
	unbind := execCtx.BindToolCall(plan)
	defer unbind()
	recoveredRecord, recovered, err := execCtx.RecoverTool(plan, w.Recover, w.GetToolSchema(call.Name))
	if err != nil || recovered {
		if err != nil {
			return nil, err
		}
		return completePlannedTool(execCtx, plan, recoveredRecord)
	}
	if err := execCtx.SetToolPlan(plan); err != nil {
		return nil, err
	}
	local := New()
	if call.Name != nativeCodeTool {
		blocked := gent.DefineTool(call.Name, func(_ context.Context, _ map[string]any) (
			*gent.ToolExecutionResponse, error,
		) {
			return &gent.ToolExecutionResponse{Status: gent.ToolBlocked,
				Instructions: "Call this tool through run_code."}, nil
		}).ParameterSchema(map[string]any{"type": "object"}).MustBuild()
		local.RegisterTool(blocked)
	} else {
		local.RegisterTool(gent.DefineTool(nativeCodeTool, func(_ context.Context, input codeInput) (
			*gent.ToolExecutionResponse, error,
		) {
			program := &programState{AttemptId: execCtx.AttemptId(), CallId: call.Id, Script: input.Code}
			if err := execCtx.SetCheckpointState("gent:program", program); err != nil {
				return nil, err
			}
			if err := execCtx.RecordingError(); err != nil {
				return nil, err
			}
			return w.executeCode(execCtx, input.Code, format)
		}).MustBuild())
	}
	args, parseErr := decodeArguments(call.RawArguments)
	record, err := local.executeInternalCall(execCtx, &parsedCall{Id: call.Id, Name: call.Name,
		ModelCallID: call.ModelCallID, ModelName: call.ModelName, RawArguments: call.RawArguments,
		Args: args, parseError: parseErr})
	if err != nil {
		return nil, err
	}
	execCtx.ClearCheckpointState("gent:program", "gent:program_calls")
	return completePlannedTool(execCtx, plan, record)
}

var _ gent.ToolChain = (*JsToolChainWrapper)(nil)
