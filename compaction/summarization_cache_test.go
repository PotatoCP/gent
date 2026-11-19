package compaction_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/agents/react"
	"github.com/rickchristie/gent/compaction"
	"github.com/rickchristie/gent/executor"
	"github.com/rickchristie/gent/format"
	"github.com/rickchristie/gent/internal/tt"
	"github.com/stretchr/testify/assert"
)

type alwaysCompact struct{}

func (alwaysCompact) ShouldCompact(*gent.ExecutionContext) (bool, error) { return true, nil }
func (alwaysCompact) NotifyCompacted(*gent.ExecutionContext) error       { return nil }

func TestSummarizationIncludesCachedTextBeforeRetention(t *testing.T) {
	type input struct{ part gent.ContentPart }
	type expected struct {
		messages [][]*gent.ModelMessage
		value    string
		err      error
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	text := gent.TextPart("Remember ABCDEFG")
	cache := &gent.CacheControl{Type: "ephemeral"}
	want := expected{messages: [][]*gent.ModelMessage{{
		gent.TextParts(gent.ChatMessageTypeSystem, compaction.DefaultSummarizationSystemPrompt),
		gent.TextParts(gent.ChatMessageTypeHuman,
			fmt.Sprintf(compaction.DefaultSummarizationUserPrompt, `## Existing Summary

None (first compaction).`, `### Message 1

Role: human
Before
Remember ABCDEFG
After

`)),
	}}, value: "Done"}
	cases := []testCase{
		{"plain text", input{text}, want},
		{"value cache", input{gent.CachedContent{ContentPart: text, CacheControl: cache}}, want},
		{"pointer cache", input{&gent.CachedContent{ContentPart: text, CacheControl: cache}}, want},
		{"nested cache", input{gent.CachedContent{ContentPart: &gent.CachedContent{
			ContentPart: &gent.TextContent{Text: "Remember ABCDEFG"}, CacheControl: cache},
			CacheControl: cache}}, want},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			codecs := gent.NewCodecRegistry()
			recorder := &nativeSummaryRecorder{codecs: codecs}
			summary := tt.NewMockModel().AddResponse("Keep ABCDEFG.", 0, 0)
			model := tt.NewMockModel().AddResponse("Done", 0, 0)
			strategy := compaction.NewSummarization(summary, format.NewXML()).
				WithUserMessageRetention(&gent.UserMessageRetentionConfig{
					MaxBytes: 1, TruncationMarker: "!"})
			runner := executor.NewRunner(react.NewAgent(model), &gent.Config{
				Codecs: codecs, Recorder: recorder, CompactionTrigger: alwaysCompact{},
				CompactionStrategy: strategy})
			execCtx, err := runner.NewExecutionContext(context.Background(), test.name)
			if !assert.NoError(t, err) {
				return
			}
			defer execCtx.Release()
			parts := []gent.ContentPart{gent.TextPart("Before"), test.input.part, gent.TextPart("After")}
			value, result, err := runner.NewTurn(execCtx, gent.NewTask(parts...))
			assert.Equal(t, test.expected, expected{summary.CapturedMessages, value, err})
			assert.Equal(t, &gent.ExecutionResult{Status: gent.ExecutionFinished,
				TurnId: execCtx.TurnId(), TerminationReason: gent.TerminationSuccess,
				Output: []gent.ContentPart{gent.TextPart("Done")}}, result)
			assert.Equal(t, [][]*gent.ModelMessage{{
				gent.TextParts(gent.ChatMessageTypeHuman, "!"),
				gent.TextParts(gent.ChatMessageTypeAI, `<prior_work_summary>
Keep ABCDEFG.
</prior_work_summary>`),
			}}, model.CapturedMessages)
			var originals []*gent.MessageContent
			for _, entry := range recorder.view.History.Entries {
				if entry.Origin == gent.RecordUserInput {
					originals = append(originals, entry.Messages...)
				}
			}
			assert.Equal(t, []*gent.MessageContent{{Role: gent.ChatMessageTypeHuman, Parts: parts}},
				originals, "compaction must retain the complete original in the archive")
		})
	}
}
