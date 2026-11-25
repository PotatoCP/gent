package models

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"image"
	"strings"
	"testing"
	"time"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/agents/react"
	"github.com/rickchristie/gent/executor"
	"github.com/rickchristie/gent/output"
	"github.com/rickchristie/gent/toolchain"
	"github.com/stretchr/testify/assert"
)

// These tests use the public service or the configured runtime, with no HTTP fixtures. Keep
// prompts small and run providers concurrently. Each provider has its own time and loop limits.
func TestLiveProviders(t *testing.T) {
	for _, provider := range liveProviders() {
		t.Run(provider.name, func(t *testing.T) {
			config := liveConfig(t, provider, false)
			t.Parallel()
			model := newLiveModel(t, provider, &config)
			t.Run("TextStream", func(t *testing.T) { checkLiveText(t, liveFeatureContext(t), model) })
			t.Run("NativeTools", func(t *testing.T) { checkLiveTools(t, liveFeatureContext(t), model) })
			checkLiveOutputs(t, model, provider.prefix != "DEEPSEEK")
		})
	}
}

// Each feature has its own deadline. A slow tool or text response must not use the time
// available for a later output test. The go test deadline still bounds the whole group.
func liveFeatureContext(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	t.Cleanup(cancel)
	return ctx
}

type liveTextCheck interface {
	assert.TestingT
	Helper()
}

func checkLiveText(t liveTextCheck, ctx context.Context, model gent.Model) {
	t.Helper()
	type expected struct {
		status  gent.ModelStatus
		text    string
		preview string
	}
	request := gent.NewModelCallRequest([]*gent.ModelMessage{
		gent.TextParts(gent.ChatMessageTypeSystem, "Follow the user's output instructions exactly."),
		gent.TextParts(gent.ChatMessageTypeHuman, "Reply with exactly gent-live-ok and nothing else."),
	}, gent.WithMaxTokens(2048))
	stream, err := model.GenerateContentStream(ctx, request)
	if !assert.NoError(t, err) {
		return
	}
	defer stream.Close()
	actual := expected{}
	var preview strings.Builder
	for chunk := range stream.Chunks() {

		for _, part := range chunk.Content {
			item, ok := part.(*gent.AssistantContent)
			if assert.True(t, ok, "provider text must have an assistant item") &&
				(item.Type == gent.AssistantContentIntermediary ||
					item.Type == gent.AssistantContentFinalAnswerCandidate) {
				preview.WriteString(gent.ContentText(item.Content))
			}
		}
	}
	actual.preview = preview.String()
	response, err := stream.Response()
	if !assert.NoError(t, err) || !assert.Len(t, response.Choices, 1) {
		return
	}
	actual.status = response.Choices[0].Completion.Status
	out := output.NewTextOutput()
	runner := executor.NewRunner(modelFixtureLoop(modelFixtureDone), &gent.Config{})
	execCtx, err := runner.NewExecutionContext(ctx, "live text output")
	if !assert.NoError(t, err) {
		return
	}
	defer execCtx.Release()
	processed, err := out.Process(execCtx, response.Choices[0].CandidateContent())
	if !assert.NoError(t, err) || !assert.True(t, processed.Accepted) {
		return
	}
	actual.text = processed.Value
	assert.Equal(t, expected{gent.ModelStatusAnswer, "gent-live-ok", "gent-live-ok"}, actual)
	if assert.NotNil(t, response.Info) {
		assert.Greater(t, response.Info.InputTokens, 0)
		assert.Greater(t, response.Info.OutputTokens, 0)
		assert.Equal(t, response.Info.InputTokens+response.Info.OutputTokens, response.Info.TotalTokens)
	}
}

func liveTask(system, prompt string) *gent.Task {
	return &gent.Task{Instructions: []*gent.ModelMessage{
		gent.TextParts(gent.ChatMessageTypeSystem, system)}, Messages: []*gent.ModelMessage{
		gent.TextParts(gent.ChatMessageTypeHuman, prompt)}}
}

func liveLimits() []gent.Limit {
	return []gent.Limit{{Type: gent.LimitExactKey, Key: gent.SCIterations, MaxValue: 4}}
}

func checkLiveTools(t *testing.T, ctx context.Context, model gent.Model) {
	t.Helper()
	type input struct {
		ID string `json:"id"`
	}
	type expected struct {
		calls       []input
		value, text string
		termination gent.TerminationReason
	}
	// Only the real tool result contains this value. A guessed answer cannot pass the test.
	secret := make([]byte, 12)
	if _, err := rand.Read(secret); !assert.NoError(t, err) {
		return
	}
	record := fmt.Sprintf("record-%x", secret)
	var calls []input
	tool := gent.DefineTool("read_live_record", func(_ context.Context, value input) (
		*gent.ToolExecutionResponse, error,
	) {
		calls = append(calls, value)
		return capturedToolResponse(record, nil)
	}).Description("Read the record for the supplied id.").MustBuild()
	out := output.NewTextOutput().SetCustomValidator("live-tool-result",
		func(_ *gent.ExecutionContext, value string) (*gent.OutputValidationResult, error) {
			if value != record {
				// Do not put the random record in steering. The model must use the real tool result,
				// including when its first answer fails to copy that result correctly.
				return &gent.OutputValidationResult{Steering: []gent.ContentPart{gent.TextPart(
					"Read the saved tool result again. Return its exact text without other text.")}}, nil
			}
			return &gent.OutputValidationResult{Accepted: true}, nil
		})
	agent := react.NewWithOutput(model, out).WithCallOptions(gent.WithMaxTokens(2048))
	runner, execCtx := newModelExecution(t, ctx, agent, &gent.Config{
		ToolChain: toolchain.New().RegisterTool(tool), Limits: liveLimits()})
	task := liveTask("Use the tools to read records.",
		`Call read_live_record exactly once with id "test". Return only the record text it supplies,
without quotes, JSON, or formatting.`)
	value, result, err := runner.NewTurn(execCtx, task)
	if !assert.NoError(t, err) {
		return
	}
	// Providers can split text into several content parts. Validate every part and the full text;
	// a fixed part count would test transport chunk boundaries instead of the accepted output.
	_, checkCtx := newModelExecution(t, ctx, modelFixtureLoop(modelFixtureDone), nil)
	processed, err := output.NewTextOutput().Process(checkCtx, result.Output)
	if !assert.NoError(t, err) || !assert.True(t, processed.Accepted) {
		return
	}
	assert.Equal(t, result.Output, processed.Content)
	want := expected{[]input{{ID: "test"}}, record, record, gent.TerminationSuccess}
	assert.Equal(t, want, expected{calls, value, processed.Value, result.TerminationReason})
	// Check the full tool result presented on each follow-up call. This distinguishes a lost
	// result from a model answer that does not use the result.
	for _, event := range providerEvents(execCtx) {
		before, ok := event.(*gent.BeforeModelCallEvent)
		if !ok || before.Iteration == 1 {
			continue
		}
		var replies [][]gent.ContentPart
		for _, message := range before.Request.Messages {
			for _, part := range message.Parts {
				if reply, ok := part.(*gent.ModelToolCallResponse); ok {
					replies = append(replies, reply.Parts())
				}
			}
		}
		assert.Equal(t, [][]gent.ContentPart{{gent.TextPart(record)}}, replies)
	}
}

type liveOutputValue struct {
	Value int `json:"value"`
}

func checkLiveOutputs(t *testing.T, model gent.Model, native bool) {
	t.Helper()
	type input struct {
		build func(gent.OutputCustomValidator[liveOutputValue]) (gent.Output[liveOutputValue], error)
	}
	type expected struct {
		value       liveOutputValue
		validations []liveOutputValue
		native      []bool
		rejected    int64
		result      *gent.ExecutionResult
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	want := expected{liveOutputValue{2}, []liveOutputValue{{1}, {2}}, []bool{native, native}, 1,
		&gent.ExecutionResult{Status: gent.ExecutionFinished, TurnId: "",
			TerminationReason: gent.TerminationSuccess,
			Output:            []gent.ContentPart{gent.TextContent{Text: `{"value":2}`}}}}
	cases := []testCase{
		{"JsonOutput", input{func(validator gent.OutputCustomValidator[liveOutputValue]) (
			gent.Output[liveOutputValue], error,
		) {
			out, err := output.NewJsonOutput[liveOutputValue]()
			if err != nil {
				return nil, err
			}
			return out.SetCustomValidator("live-correction", validator), nil
		}}, want},
		{"SchemaOutput", input{func(validator gent.OutputCustomValidator[liveOutputValue]) (
			gent.Output[liveOutputValue], error,
		) {
			out, err := output.NewSchemaOutput[liveOutputValue](providerOutputSchema())
			if err != nil {
				return nil, err
			}
			return out.SetCustomValidator("live-correction", validator), nil
		}}, want},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			ctx := liveFeatureContext(t)
			actual := expected{}
			out, err := test.input.build(func(_ *gent.ExecutionContext, value liveOutputValue) (
				*gent.OutputValidationResult, error,
			) {
				actual.validations = append(actual.validations, value)
				if len(actual.validations) == 1 || value.Value != 2 {
					return &gent.OutputValidationResult{Steering: []gent.ContentPart{gent.TextContent{
						Text: `The required value is now 2. Return {"value":2}.`,
					}}}, nil
				}
				return &gent.OutputValidationResult{Accepted: true}, nil
			})
			if !assert.NoError(t, err) {
				return
			}
			agent := react.NewWithOutput(model, out).WithCallOptions(gent.WithMaxTokens(2048))
			runner, execCtx := newModelExecution(t, ctx, agent, &gent.Config{Limits: liveLimits()})
			actual.value, actual.result, err = runner.NewTurn(execCtx,
				liveTask(out.Instructions(), `Return {"value":1}.`))
			if !assert.NoError(t, err) {
				return
			}
			for _, event := range providerEvents(execCtx) {
				after, ok := event.(*gent.AfterModelCallEvent)
				if !ok {
					continue
				}
				fields, ok := after.Response.Info.RawGenerationInfo["output_schema"].(map[string]any)
				if !assert.True(t, ok, "schema enforcement decision is required") {
					return
				}
				enforced, ok := fields["native"].(bool)
				if !assert.True(t, ok, "native enforcement decision is required") {
					return
				}
				actual.native = append(actual.native, enforced)
			}
			actual.rejected = execCtx.Stats().GetCounter(gent.SCOutputCustomRejectedTotal)
			want := test.expected
			completed := *want.result
			completed.TurnId = execCtx.TurnId()
			want.result = &completed
			assert.Equal(t, want, actual)
		})
	}
}

// Image models are separate because text-only models cannot exercise image generation. Use a
// Responses model with the image tool for OpenAI/xAI, and an image model for Gemini/OpenRouter.
// A solid square can end with an IMAGE_RECITATION refusal before the validator receives an image.
// Request an original scene and then a specific edit to exercise both validation calls.
func TestLiveImages(t *testing.T) {
	for _, provider := range liveProviders() {
		if !provider.images {
			continue
		}
		t.Run(provider.name, func(t *testing.T) {
			config := liveConfig(t, provider, true)
			t.Parallel()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			out := output.NewImageOutput().SetAllowedMIMETypes("image/jpeg").
				SetMaxFileSizeBytes(8 << 20).SetMaxWidth(2048).SetMaxHeight(2048)
			validations := 0
			out.SetCustomValidator("live-image-correction", func(_ *gent.ExecutionContext,
				_ []gent.BinaryContent,
			) (*gent.OutputValidationResult, error) {
				validations++
				if validations == 1 {
					return &gent.OutputValidationResult{Steering: []gent.ContentPart{gent.TextPart(
						"Add a green leaf beside the can. Return only the new JPEG image, with no text.")}}, nil
				}
				return &gent.OutputValidationResult{Accepted: true}, nil
			})
			task := liveTask(out.Instructions(),
				`Draw one original 512 by 512 illustration of an orange watering can on a blue stool.
Place two white stones beside the stool. Return only the JPEG image.
The image_generation tool output is the complete answer. Do not send any assistant text,
including a caption, a link, a description, or a completion message.`)
			agent := react.NewWithOutput(newLiveModel(t, provider, &config), out).
				WithCallOptions(gent.WithMaxTokens(2048))
			runner, execCtx := newModelExecution(t, ctx, agent, &gent.Config{Limits: liveLimits()})
			defer logLiveImageFailure(t, execCtx)
			values, result, err := runner.NewTurn(execCtx, task)
			if !assert.NoError(t, err) || !assert.Len(t, values, 1) {
				return
			}
			assert.Equal(t, 2, validations)
			assert.Equal(t, int64(1), execCtx.Stats().GetCounter(gent.SCOutputCustomRejectedTotal))
			assert.Equal(t, &gent.ExecutionResult{Status: gent.ExecutionFinished, TurnId: execCtx.TurnId(),
				TerminationReason: gent.TerminationSuccess,
				Output:            []gent.ContentPart{values[0]}}, result)
			assert.Equal(t, "image/jpeg", values[0].MIMEType)
			assert.Greater(t, len(values[0].Data), 0)
			assert.LessOrEqual(t, len(values[0].Data), 8<<20)
			decoded, format, err := image.DecodeConfig(bytes.NewReader(values[0].Data))
			if !assert.NoError(t, err) {
				return
			}
			assert.Equal(t, "jpeg", format)
			assert.Greater(t, decoded.Width, 0)
			assert.Greater(t, decoded.Height, 0)
			assert.LessOrEqual(t, decoded.Width, 2048)
			assert.LessOrEqual(t, decoded.Height, 2048)
		})
	}
}

// Report validation and part sizes so a live failure can be diagnosed without image bytes,
// provider continuation data, or credentials in the test log.
func logLiveImageFailure(t *testing.T, execCtx *gent.ExecutionContext) {
	t.Helper()
	if !t.Failed() {
		return
	}
	for _, event := range providerEvents(execCtx) {
		switch event := event.(type) {
		case *gent.OutputValidationEvent:
			t.Logf("image validation: iteration=%d stage=%s accepted=%t steering=%q error=%v",
				event.Iteration, event.Stage, event.Accepted, gent.ContentText(event.Steering), event.Error)
		case *gent.AfterModelCallEvent:
			if event.Error != nil {
				t.Logf("image call: iteration=%d error=%v", event.Iteration, event.Error)
				continue
			}
			for _, choice := range event.Response.Choices {
				var parts []string
				for _, part := range choice.CandidateContent() {
					switch part := part.(type) {
					case gent.BinaryContent:
						parts = append(parts, fmt.Sprintf("%s: %d bytes", part.MIMEType, len(part.Data)))
					case gent.TextContent:
						parts = append(parts, fmt.Sprintf("text: %d bytes", len(part.Text)))
						t.Logf("image text: %q", part.Text)
					default:
						parts = append(parts, fmt.Sprintf("%T", part))
					}
				}
				t.Logf("image completion: iteration=%d status=%s reason=%q parts=%v",
					event.Iteration, choice.Completion.Status, choice.Completion.ProviderReason, parts)
			}
		}
	}
}
