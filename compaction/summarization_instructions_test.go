package compaction_test

import (
	"context"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/agents/react"
	"github.com/rickchristie/gent/compaction"
	"github.com/rickchristie/gent/executor"
	"github.com/rickchristie/gent/format"
	"github.com/rickchristie/gent/internal/tt"
	"github.com/stretchr/testify/assert"
)

func TestSummarizationIncludesInstructionParts(t *testing.T) {
	type input struct{ part gent.ContentPart }
	type expected struct {
		messages []*gent.ModelMessage
		answer   string
		err      error
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	type partCase struct {
		name string
		part gent.ContentPart
	}
	data, err := gent.DataPart(map[string]any{"currency": "IDR"})
	if !assert.NoError(t, err) {
		return
	}
	dataValue := data.(gent.DataContent)
	image := gent.BinaryContent{MIMEType: "image/png", Data: []byte{1, 2, 3}}
	parts := []partCase{
		{"data value", data}, {"data pointer", &dataValue},
		{"URL value", gent.ImageURLContent{URL: "https://example.com/rules.png", Detail: "high"}},
		{"URL pointer", &gent.ImageURLContent{URL: "https://example.com/policy.png", Detail: "low"}},
		{"image", image},
		{"audio", &gent.BinaryContent{MIMEType: "audio/wav", Data: []byte{4, 5, 6}}},
		{"video", gent.BinaryContent{MIMEType: "video/mp4", Data: []byte{7, 8, 9}}},
		{"document", &gent.BinaryContent{MIMEType: "application/pdf", Data: []byte{10, 11, 12}}},
	}
	cache := &gent.CacheControl{Type: "ephemeral"}
	cached := gent.CachedContent{ContentPart: data, CacheControl: cache}
	parts = append(parts, partCase{"cached value", cached}, partCase{"cached pointer", &cached},
		partCase{"nested cache", gent.CachedContent{
			ContentPart:  &gent.CachedContent{ContentPart: image, CacheControl: cache},
			CacheControl: cache}})
	var cases []testCase
	for _, part := range parts {
		cases = append(cases, testCase{name: part.name, input: input{part.part},
			expected: expected{answer: "Done", messages: []*gent.ModelMessage{
				gent.TextParts(gent.ChatMessageTypeHuman, `## Existing Summary

None (first compaction).## Current System Instructions (read-only context)

Role: system
Use the current business rules.
Role: system

### Message 1

Role: human
Check the order.

`), {Role: gent.ChatMessageTypeHuman, Parts: []gent.ContentPart{
					gent.TextPart("Attached context from read-only system instructions, message 1, role system:"),
					part.part,
				}},
			}}})
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			codecs := gent.NewCodecRegistry()
			recorder := &nativeSummaryRecorder{codecs: codecs}
			summary := tt.NewMockModel().AddResponse("Keep the business rules.", 0, 0)
			model := tt.NewMockModel().AddResponse("Done", 0, 0)
			strategy := compaction.NewSummarization(summary, format.NewXML()).WithPrompt("%s%s")
			runner := executor.NewRunner(react.NewAgent(model), &gent.Config{
				Codecs: codecs, Recorder: recorder, CompactionTrigger: alwaysCompact{},
				CompactionStrategy: strategy})
			execCtx, err := runner.NewExecutionContext(context.Background(), test.name)
			if !assert.NoError(t, err) {
				return
			}
			defer execCtx.Release()
			task := gent.NewTask(gent.TextPart("Check the order."))
			task.Instructions = []*gent.ModelMessage{
				gent.TextParts(gent.ChatMessageTypeSystem, "Use the current business rules."),
				{Role: gent.ChatMessageTypeSystem, Parts: []gent.ContentPart{test.input.part}},
			}
			answer, result, err := runner.NewTurn(execCtx, task)
			if !assert.NoError(t, err) || !assert.Len(t, summary.CapturedMessages, 1) {
				return
			}
			assert.Equal(t, test.expected, expected{summary.CapturedMessages[0], answer, err})
			assert.Equal(t, &gent.ExecutionResult{Status: gent.ExecutionFinished,
				TurnId: execCtx.TurnId(), TerminationReason: gent.TerminationSuccess,
				Output: []gent.ContentPart{gent.TextPart("Done")}}, result)
			assert.Equal(t, task.Instructions, execCtx.Instructions())
			assert.Equal(t, task.Instructions, recorder.view.Checkpoint.Contexts[0].Instructions)
			assert.Equal(t, task.Instructions, model.CapturedMessages[0][:2])
			var saved [][]*gent.ModelMessage
			for _, event := range recorder.view.Activity {
				call, ok := event.Payload.(*gent.BeforeModelCallEvent)
				if ok && call.StreamTopicId == "compaction" {
					saved = append(saved, call.Request.Messages)
				}
			}
			assert.Equal(t, [][]*gent.ModelMessage{test.expected.messages}, saved,
				"the saved request must show the actual instruction content sent to the summarizer")
		})
	}
}
