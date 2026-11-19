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

// PrepareRequest resolves SharedLocalSearch and native definitions for this request.
func (m *generateContentModel) PrepareRequest(execCtx *gent.ExecutionContext,
	request *gent.ModelCallRequest, chain gent.ToolChain,
) (*gent.ModelCallRequest, error) {
	return gent.PrepareModelRequest(execCtx, request, chain)
}

// PrepareRequest resolves SharedLocalSearch and native definitions for this request.
func (m *Ollama) PrepareRequest(execCtx *gent.ExecutionContext, request *gent.ModelCallRequest,
	chain gent.ToolChain,
) (*gent.ModelCallRequest, error) {
	return gent.PrepareModelRequest(execCtx, request, chain)
}
