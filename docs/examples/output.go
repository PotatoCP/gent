package examples

import (
	"fmt"
	"strings"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/agents/react"
	"github.com/rickchristie/gent/executor"
	"github.com/rickchristie/gent/output"
	"github.com/rickchristie/gent/schema"
)

// docs:start output-type
type Answer struct {
	Summary string `json:"summary"`
	Count   int    `json:"count"`
}

// docs:end output-type

func jsonRunner(model gent.Model) (*executor.Runner[Answer], error) {
	// docs:start output-json
	out, err := output.NewJsonOutput[Answer](schema.Field("count").Min(0))
	if err != nil {
		return nil, err
	}
	loop := react.NewWithOutput(model, out)
	runner := executor.NewRunner(loop, &gent.Config{Limits: gent.DefaultLimits()})
	// NewTurn now returns Answer instead of string.
	// docs:end output-json
	return runner, nil
}

func schemaOutput() (*output.SchemaOutput[Answer], error) {
	// docs:start output-schema
	out, err := output.NewSchemaOutput[Answer](map[string]any{
		"type": "object",
		"properties": map[string]any{
			"summary": map[string]any{"type": "string"},
			"count":   map[string]any{"type": "integer", "minimum": 0},
		},
		"required":             []string{"summary", "count"},
		"additionalProperties": false,
	})
	// docs:end output-schema
	return out, err
}

func textOutput() *output.TextOutput {
	// docs:start output-validator
	out := output.NewTextOutput().SetCustomValidator("nonempty",
		func(execCtx *gent.ExecutionContext, value string) (*gent.OutputValidationResult, error) {
			if strings.TrimSpace(value) == "" {
				return &gent.OutputValidationResult{
					Steering: []gent.ContentPart{gent.TextPart("Return a non-empty answer.")},
				}, nil
			}
			return &gent.OutputValidationResult{Accepted: true}, nil
		})
	// The built-in checks run first. The validator must not change the value.
	// docs:end output-validator
	return out
}

func imageOutput() *output.ImageOutput {
	// docs:start output-image
	out := output.NewImageOutput().
		SetMaxFileSizeBytes(4 << 20).
		SetMaxWidth(1024).
		SetMaxHeight(1024).
		SetAllowedMIMETypes("image/png")
	// A provider must support image output. Gent validates complete image bytes locally.
	// docs:end output-image
	return out
}

// docs:start output-custom
type TrimmedOutput struct{}

func (*TrimmedOutput) Instructions() string { return "Return a short text answer." }

func (*TrimmedOutput) Configure(request *gent.ModelCallRequest) error {
	return output.NewTextOutput().Configure(request)
}

func (*TrimmedOutput) Process(execCtx *gent.ExecutionContext, parts []gent.ContentPart) (
	*gent.OutputResult[string], error,
) {
	checked, err := output.NewTextOutput().Process(execCtx, parts)
	if err != nil || !checked.Accepted {
		return checked, err
	}
	value := strings.TrimSpace(checked.Value)
	return &gent.OutputResult[string]{Accepted: true, Value: value,
		Content: []gent.ContentPart{gent.TextPart(value)}}, nil
}

// docs:end output-custom

func outputInstructions(out gent.Output[string]) *gent.Task {
	// docs:start output-instructions
	task := &gent.Task{
		Instructions: []*gent.ModelMessage{
			gent.TextParts(gent.ChatMessageTypeSystem, out.Instructions()),
		},
		Messages: gent.NewTask(gent.TextPart("Describe the result.")).Messages,
	}
	// The caller chooses to include these words. Native controls do not depend on them.
	// docs:end output-instructions
	return task
}

func checkOutputResult(result *gent.ExecutionResult) error {
	if result.Status != gent.ExecutionFinished ||
		result.TerminationReason != gent.TerminationSuccess {
		return fmt.Errorf("answer unavailable: %s / %s", result.Status, result.TerminationReason)
	}
	return nil
}
