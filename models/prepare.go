package models

import "github.com/rickchristie/gent"

// PrepareRequest resolves SharedLocalSearch and native definitions for this request.
func (m *Responses) PrepareRequest(execCtx *gent.ExecutionContext, request *gent.ModelCallRequest,
	chain gent.ToolChain,
) (*gent.ModelCallRequest, error) {
	return gent.PrepareModelRequest(execCtx, request, chain)
}

// PrepareRequest resolves SharedLocalSearch and native definitions for this request.
func (m *Chat) PrepareRequest(execCtx *gent.ExecutionContext, request *gent.ModelCallRequest,
	chain gent.ToolChain,
) (*gent.ModelCallRequest, error) {
	return gent.PrepareModelRequest(execCtx, request, chain)
}

// PrepareRequest resolves SharedLocalSearch and native definitions for this request.
func (m *Anthropic) PrepareRequest(execCtx *gent.ExecutionContext, request *gent.ModelCallRequest,
	chain gent.ToolChain,
) (*gent.ModelCallRequest, error) {
	return gent.PrepareModelRequest(execCtx, request, chain)
}

// PrepareRequest resolves the catalog and checks the effective Gemini function declarations.
func (m *generateContentModel) PrepareRequest(execCtx *gent.ExecutionContext,
	request *gent.ModelCallRequest, chain gent.ToolChain,
) (*gent.ModelCallRequest, error) {
	return prepareFunctionTools(execCtx, request, chain, checkGeminiFunctions)
}

// PrepareRequest resolves the catalog and checks the effective Ollama function declarations.
func (m *Ollama) PrepareRequest(execCtx *gent.ExecutionContext, request *gent.ModelCallRequest,
	chain gent.ToolChain,
) (*gent.ModelCallRequest, error) {
	return prepareFunctionTools(execCtx, request, chain, checkOllamaFunctions)
}

// A catalog replaces explicit tools. Check the resolved definitions here and check again at
// generation because direct callers and BeforeModelCall Hooks can supply different definitions.
func prepareFunctionTools(execCtx *gent.ExecutionContext, request *gent.ModelCallRequest,
	chain gent.ToolChain, check func([]*gent.ToolDefinition) error,
) (*gent.ModelCallRequest, error) {
	prepared, err := gent.PrepareModelRequest(execCtx, request, chain)
	if err != nil {
		return nil, err
	}
	if err := check(prepared.Options.Tools); err != nil {
		return nil, err
	}
	return prepared, nil
}
