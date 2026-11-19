package examples

import (
	"context"
	"errors"
	"fmt"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/agents/react"
	"github.com/rickchristie/gent/executor"
	"github.com/rickchristie/gent/schema"
	"github.com/rickchristie/gent/toolchain"
)

func toolsExample(model gent.Model) *executor.Runner[string] {
	// docs:start tools-add
	type AddInput struct {
		A float64 `json:"a"`
		B float64 `json:"b"`
	}
	add := gent.DefineTool("add",
		func(ctx context.Context, input *AddInput) (*gent.ToolExecutionResponse, error) {
			return &gent.ToolExecutionResponse{
				Status: gent.ToolExecuted,
				Parts: []gent.ContentPart{
					gent.TextPart(fmt.Sprint(input.A + input.B)),
				},
			}, nil
		}).Description("Add two numbers.").MustBuild()
	// MustBuild rejects an invalid fixed definition during application setup.

	chain := toolchain.New()
	chain.RegisterTool(add)
	loop := react.NewAgent(model)
	runner := executor.NewRunner(loop, &gent.Config{
		ToolChain: chain,
		Limits:    gent.DefaultLimits(),
	})
	// docs:end tools-add
	return runner
}

type lookupInput struct {
	ID string `json:"id"`
}

type orderService struct{}

func (service *orderService) Lookup(ctx context.Context, input *lookupInput) (
	*gent.ToolExecutionResponse, error,
) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &gent.ToolExecutionResponse{Status: gent.ToolExecuted,
		Parts: []gent.ContentPart{gent.TextPart(input.ID)}}, nil
}

func boundTool(service *orderService) (gent.Tool[*lookupInput], error) {
	// docs:start tools-method
	tool, err := gent.DefineTool("lookup_order", service.Lookup).
		Description("Read the order with the supplied ID.").Build()
	if err != nil {
		return nil, err
	}
	// docs:end tools-method
	return tool, nil
}

func structuredTool(sum float64) (*gent.ToolExecutionResponse, error) {
	// docs:start tools-data
	parts, err := gent.NewParts().Data(map[string]any{"sum": sum}).Build()
	if err != nil {
		return nil, err
	}
	return &gent.ToolExecutionResponse{
		Status:       gent.ToolExecuted,
		Parts:        parts,
		Instructions: "Use the returned sum in your answer.",
	}, nil
	// docs:end tools-data
}

func blockedTool() (*gent.ToolExecutionResponse, error) {
	// docs:start tools-blocked
	// Return before performing an action that needs application approval.
	return &gent.ToolExecutionResponse{
		Status:       gent.ToolBlocked,
		Instructions: "Ask the user to approve the shipment.",
	}, nil
	// docs:end tools-blocked
}

func invalidTool() (*gent.ToolExecutionResponse, error) {
	// docs:start tools-invalid
	return &gent.ToolExecutionResponse{
		Status:       gent.ToolInvalidArguments,
		Instructions: "Supply an order ID from the current account.",
	}, nil
	// docs:end tools-invalid
}

func failedTool() gent.Tool[struct{}] {
	// docs:start tools-error
	tool := gent.DefineTool("read_orders",
		func(ctx context.Context, input struct{}) (*gent.ToolExecutionResponse, error) {
			// The raw error is diagnostic data. It is not model guidance.
			return nil, errors.New("order store connection failed")
		}).UnrecoverableErrorInstructions("Order lookup failed. Ask the user to try later.").
		MustBuild()
	// docs:end tools-error
	return tool
}

func schemaExamples() (gent.Tool[*lookupInput], error) {
	service := &orderService{}
	// docs:start schema-fields
	tool, err := gent.DefineTool("lookup_order", service.Lookup).
		Parameters(schema.Field("id").Description("The order ID.").MinLength(1)).
		Strict(true).Build()
	if err != nil {
		return nil, err
	}
	// docs:end schema-fields
	return tool, nil
}

// docs:start schema-optional
type SearchInput struct {
	Limit int `json:"limit,omitempty"`
}

// docs:end schema-optional

func optionalTool() (gent.Tool[SearchInput], error) {
	// docs:start schema-optional-build
	tool, err := gent.DefineTool("search", func(ctx context.Context, input SearchInput) (
		*gent.ToolExecutionResponse, error,
	) {
		return structuredTool(float64(input.Limit))
	}).Parameters(schema.Field("limit").Optional().Min(0)).Build()
	// Optional permits absence. The JSON tag alone does not do this.
	// docs:end schema-optional-build
	return tool, err
}

func explicitSchema() (gent.Tool[*lookupInput], error) {
	service := &orderService{}
	// docs:start schema-explicit
	tool, err := gent.DefineTool("lookup_order", service.Lookup).
		ParameterSchema(map[string]any{
			"type":                 "object",
			"properties":           map[string]any{"id": map[string]any{"type": "string"}},
			"required":             []string{"id"},
			"additionalProperties": false,
		}).Build()
	// Do not combine ParameterSchema with Parameters field overrides.
	// docs:end schema-explicit
	return tool, err
}

func outputSchema() (gent.Tool[*lookupInput], error) {
	service := &orderService{}
	// docs:start schema-output
	type Order struct {
		ID string `json:"id"`
	}
	tool, err := gent.DefineTool("lookup_order", service.Lookup).
		OutputSchemaFrom(Order{}).Build()
	// This is model guidance. The handler must still check its returned domain data.
	// docs:end schema-output
	return tool, err
}
