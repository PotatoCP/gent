package multiturntest

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

type eachIterationCompaction struct{}

func (eachIterationCompaction) ShouldCompact(
	*gent.ExecutionContext,
) (
	bool,
	error,
) {
	return true, nil
}
func (eachIterationCompaction) NotifyCompacted(*gent.ExecutionContext) error { return nil }

func TestRetainedUsersSurviveCompactionAndRestoration(t *testing.T) {
	model := tt.NewMockModel().AddResponse("First answer", 10, 2).AddResponse("Second answer", 20, 2)
	summary := tt.NewMockModel().WithName("summary").AddResponse("Summary one", 10, 2).
		AddResponse("Summary two", 10, 2)
	strategy := compaction.NewSummarization(summary, format.NewXML()).
		WithUserMessageRetention(&gent.UserMessageRetentionConfig{MaxBytes: 6, TruncationMarker: "!"})
	codecs := gent.NewCodecRegistry()
	recorder := &memoryRecorder{codecs: codecs}
	runner := executor.NewRunner(
		react.NewAgent(
			model,
		),
		&gent.Config{
			Codecs:             codecs,
			Recorder:           recorder,
			CompactionTrigger:  eachIterationCompaction{},
			CompactionStrategy: strategy,
		},
	)
	execCtx, err := runner.NewExecutionContext(context.Background(), "retention")
	if !assert.NoError(t, err) {
		return
	}
	defer execCtx.Release()
	prefix := gent.TextParts(gent.ChatMessageTypeSystem, "Use the saved task.")
	_, _, err = runner.NewTurn(execCtx, &gent.Task{Instructions: []*gent.ModelMessage{prefix},
		Messages: gent.NewTask(gent.TextPart("ABCDEFGHIJ")).Messages})
	if !assert.NoError(t, err) {
		return
	}
	loaded, err := codecs.UnmarshalRunState(recorder.states[len(recorder.states)-1])
	if !assert.NoError(t, err) || !assert.Equal(t, gent.RunStateReady, loaded.Status) {
		return
	}
	restored, err := runner.Restore(context.Background(), loaded.State)
	if !assert.NoError(t, err) || !assert.Equal(t, gent.RestoreReady, restored.Status) {
		return
	}
	defer restored.Context.Release()
	_, _, err = runner.NewTurn(restored.Context, gent.NewTask(gent.TextPart("klmno")))
	if !assert.NoError(t, err) {
		return
	}
	assert.Equal(t, [][]*gent.ModelMessage{
		{prefix, gent.TextParts(gent.ChatMessageTypeHuman, "!", "FGHIJ"),
			gent.TextParts(gent.ChatMessageTypeAI, `<prior_work_summary>
Summary one
</prior_work_summary>`)},
		{prefix, gent.TextParts(gent.ChatMessageTypeHuman, "!", "klmno"),
			gent.TextParts(gent.ChatMessageTypeAI, `<prior_work_summary>
Summary two
</prior_work_summary>`)},
	}, model.CapturedMessages)
	firstSummaryInput := fmt.Sprintf(compaction.DefaultSummarizationUserPrompt,
		`## Existing Summary

None (first compaction).`, `## Current System Instructions (read-only context)

Role: system
Use the saved task.

### Message 1

Role: human
ABCDEFGHIJ

`)
	assert.Equal(t, []*gent.ModelMessage{
		gent.TextParts(gent.ChatMessageTypeSystem, compaction.DefaultSummarizationSystemPrompt),
		gent.TextParts(gent.ChatMessageTypeHuman, firstSummaryInput),
	}, summary.CapturedMessages[0])
	assert.Equal(t, 2, summary.CallCount())
	assert.Equal(t, []gent.ExecutionPhase{gent.PhaseInitial, gent.PhaseCompactionCompleted,
		gent.PhaseIterationReady, gent.PhaseTerminal, gent.PhaseInitial, gent.PhaseCompactionCompleted,
		gent.PhaseIterationReady, gent.PhaseTerminal}, recorder.phases)
	full, view := gent.NewRunState(execCtx.RunId()), gent.NewRunView(execCtx.RunId())
	for i, data := range recorder.batches {
		var executionBatch, archiveBatch gent.SaveBatch
		if !assert.NoError(t, codecs.Unmarshal(data, &executionBatch)) ||
			!assert.NoError(t, codecs.Unmarshal(data, &archiveBatch)) {
			return
		}
		applied, err := full.Apply(&executionBatch, codecs)
		assert.NoError(t, err)
		assert.Equal(t, &gent.ApplyStateResult{Status: gent.ApplyApplied}, applied)
		applied, err = view.Apply(&archiveBatch, codecs)
		assert.NoError(t, err)
		assert.Equal(t, &gent.ApplyStateResult{Status: gent.ApplyApplied}, applied)
		encoded, err := codecs.MarshalRunState(full)
		assert.NoError(t, err)
		assert.Equal(t, recorder.states[i], encoded)
	}
	assert.Equal(t, "ABCDEFGHIJ", gent.ContentText(view.History.Entries[0].Messages[0].Parts))
}
