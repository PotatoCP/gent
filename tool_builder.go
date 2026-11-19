package gent

import (
	"context"
	core "github.com/rickchristie/gent/internal/core"
)

// ToolBuilder prepares a tool before registration. Setters transfer ownership of retained values.
// A builder is for setup in one goroutine; built tools can be shared between runs.
type ToolBuilder[I any] = core.ToolBuilder[I]

// DefineTool starts a tool definition. The function can be a bound method on a service.
// Use Build to check the definition, or MustBuild for fixed application configuration.
func DefineTool[I any](
	name string, call func(context.Context, I) (*ToolExecutionResponse, error),
) *ToolBuilder[I] {
	return core.DefineTool[I](name, call)
}
