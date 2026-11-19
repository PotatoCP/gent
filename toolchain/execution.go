package toolchain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/internal/jsonfields"
	"github.com/rickchristie/gent/schema"
)

type callObservation struct {
	response       *gent.ToolExecutionResponse
	diagnostic     error
	invalidDetails string
	safeError      string
	input          any
	duration       time.Duration
}

// A returned author-tool failure is handled evidence. An invocation error is a framework
// failure and must leave the accepted operation unfinished for restoration.
type toolCallReturn struct {
	response   *gent.ToolExecutionResponse
	diagnostic error
}

func (c *Registry) executeCall(execCtx *gent.ExecutionContext, call *parsedCall) (
	*gent.ToolCallRecord, error,
) {
	return c.executeCallWith(execCtx, call, func(ctx context.Context, tool, input any) (
		*toolCallReturn, error,
	) {
		response, err := CallToolWithTypedInputReflect(ctx, tool, input)
		return &toolCallReturn{response: response, diagnostic: err}, nil
	})
}

// Internal tools run framework operations under the same validation and save barriers. Their
// errors must reach the application before any result publication or tool-completed save.
func (c *Registry) executeInternalCall(execCtx *gent.ExecutionContext, call *parsedCall) (
	*gent.ToolCallRecord, error,
) {
	return c.executeCallWith(execCtx, call, func(ctx context.Context, tool, input any) (
		*toolCallReturn, error,
	) {
		response, err := CallToolWithTypedInputReflect(ctx, tool, input)
		return &toolCallReturn{response: response}, err
	})
}

func (c *Registry) executeCallWith(execCtx *gent.ExecutionContext, call *parsedCall,
	invoke func(context.Context, any, any) (*toolCallReturn, error),
) (
	*gent.ToolCallRecord, error,
) {
	tool := c.toolMap[call.Name]
	compiled := c.schemaMap[call.Name]
	observation := callObservation{}
	if tool == nil {
		observation.diagnostic = fmt.Errorf("%w: %s", gent.ErrUnknownTool, call.Name)
		observation.safeError = execCtx.SteeringMessages().ToolUnavailable
	} else if call.parseError != nil {
		observation.diagnostic = call.parseError
		observation.invalidDetails = safeConversionDetails(call.parseError)
	} else if err := compiled.Validate(call.Args); err != nil {
		observation.diagnostic, observation.invalidDetails = err, schema.SafeValidationDetails(err)
	} else {
		input, err := TransformArgsReflect(tool, call.Args)
		observation.input = input
		if err != nil {
			observation.diagnostic, observation.invalidDetails = err, safeConversionDetails(err)
		}
	}
	beforeArgs := observation.input
	if beforeArgs == nil {
		beforeArgs = call.Args
	}
	before := execCtx.PublishBeforeToolCall(call.Name, beforeArgs, gent.WithToolCallId(call.Id),
		gent.WithToolCallInputValidation(func(input any) error {
			if observation.diagnostic != nil {
				return nil // Original model argument failures retain their correction path.
			}
			return validateEffectiveInput(tool, compiled, input)
		}))
	if err := execCtx.RecordingError(); err != nil {
		return nil, err
	}
	if err := execCtx.Context().Err(); err != nil {
		return nil, err
	}
	if observation.diagnostic == nil {
		observation.input = before.Args
		start := time.Now()
		returned, err := invoke(execCtx.ToolContext(before.ToolCallId), tool, before.Args)
		if err != nil {
			return nil, fmt.Errorf("gent: internal tool %q in run %q turn %q context %q call %q: %w",
				call.Name, execCtx.RunId(), execCtx.TurnId(), execCtx.ContextId(), before.ToolCallId, err)
		}
		observation.response, observation.diagnostic = returned.response, returned.diagnostic
		observation.duration = time.Since(start)
	}
	if execCtx.IsWorkerInterrupted() {
		return nil, execCtx.Context().Err()
	}
	if tool != nil && observation.safeError == "" {
		observation.safeError = tool.(toolDefinition).UnrecoverableErrorInstructions()
	}
	result, err := buildCallRecord(execCtx, call, compiled, observation)
	if err != nil {
		return nil, err
	}
	execCtx.PublishAfterToolCall(call.Name, observation.input, result, observation.duration,
		observation.diagnostic, gent.WithToolCallId(call.Id), gent.WithToolCallSource(before.Source))
	return result, execCtx.RecordingError()
}

func validateEffectiveInput(tool any, compiled *schema.Schema, input any) error {
	want := reflect.ValueOf(tool).MethodByName("Call").Type().In(1)
	if reflect.TypeOf(input) != want {
		return fmt.Errorf("effective input has type %T, expected %s", input, want)
	}
	part, err := gent.DataPart(input)
	if err != nil {
		return err
	}
	value := part.(gent.DataContent).Value()
	// Duration input uses a readable schema string but is a Go nanosecond count after conversion.
	var fields jsonfields.Resolver
	value, err = validationValue(value, reflect.ValueOf(input), &fields)
	if err != nil {
		return err
	}
	return compiled.Validate(value)
}

func validationValue(value any, original reflect.Value, fields *jsonfields.Resolver) (any, error) {
	// Custom encoders can change shape, order, and numeric meaning. Validate their emitted JSON
	// as supplied; temporal conversion only applies to ordinary Go fields and elements.
	original = jsonfields.Source(original)
	if !original.IsValid() {
		return value, nil
	}
	if original.Type() == reflect.TypeFor[time.Duration]() {
		return time.Duration(original.Int()).String(), nil
	}
	switch result := value.(type) {
	case map[string]any:
		sources, err := fields.Object(original)
		if err != nil {
			return nil, err
		}
		for name, item := range result {
			result[name], err = validationValue(item, sources[name], fields)
			if err != nil {
				return nil, err
			}
		}
	case []any:
		if original.Kind() != reflect.Array && original.Kind() != reflect.Slice {
			return value, nil
		}
		for i, item := range result {
			var err error
			result[i], err = validationValue(item, original.Index(i), fields)
			if err != nil {
				return nil, err
			}
		}
	}
	return value, nil
}

func safeConversionDetails(err error) string {
	var syntax *json.SyntaxError
	if errors.As(err, &syntax) {
		return fmt.Sprintf("args: invalid JSON at byte %d.", syntax.Offset)
	}
	var mismatch *json.UnmarshalTypeError
	if errors.As(err, &mismatch) {
		path := "args"
		if mismatch.Field != "" {
			path += "." + mismatch.Field
		}
		return fmt.Sprintf("%s: expected a value compatible with %s (byte %d).",
			path, mismatch.Type, mismatch.Offset)
	}
	return "args: expected a complete JSON object with values compatible with the input schema."
}

func buildCallRecord(execCtx *gent.ExecutionContext, call *parsedCall, compiled *schema.Schema,
	observation callObservation,
) (*gent.ToolCallRecord, error) {
	returned, err := execCtx.SnapshotDiagnostic(observation.response)
	if err != nil {
		return nil, err
	}
	input, err := execCtx.SnapshotDiagnostic(observation.input)
	if err != nil {
		return nil, err
	}
	response := observation.response
	if observation.invalidDetails != "" {
		response = &gent.ToolExecutionResponse{Status: gent.ToolInvalidArguments,
			Instructions: observation.invalidDetails}
	} else if observation.diagnostic == nil {
		observation.diagnostic = gent.ValidateToolExecution(response)
	}
	errorPath := observation.diagnostic != nil && observation.invalidDetails == ""
	if !errorPath {
		// Normalization changes a public author response without changing its diagnostic snapshot.
		normalized := *response
		if normalized.Status != gent.ToolExecuted {
			normalized.Parts = nil
		}
		if normalized.Status == gent.ToolInvalidArguments {
			details := normalized.Instructions
			if details == "" {
				details = "args: the supplied input was rejected."
			}
			normalized.Instructions = compiled.Correction(call.Name, details)
		}
		response = &normalized
	}
	diagnostic, err := execCtx.SnapshotDiagnostic(observation.diagnostic)
	if err != nil {
		return nil, err
	}
	builder := gent.NewToolCallRecordBuilder(call.Id, execCtx.AttemptId(), call.Name).
		Arguments(call.RawArguments, call.Args).Input(input).ReturnedResult(returned).Error(diagnostic).
		DiscoveredTools(call.discovered...)
	var parts []gent.ContentPart
	if errorPath {
		builder.CallError()
		text := observation.safeError
		if text == "" {
			text = execCtx.SteeringMessages().ToolError
		}
		parts = []gent.ContentPart{gent.TextPart(text)}
	} else {
		builder.Execution(response)
		parts = gent.PrepareExecutionParts(response, execCtx.SteeringMessages())
	}
	return builder.ModelReply(call.ModelCallID, call.ModelName, parts...).Build()
}
