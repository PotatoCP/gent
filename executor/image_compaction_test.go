package executor_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/agents"
	"github.com/rickchristie/gent/executor"
	"github.com/rickchristie/gent/internal/tt"
	"github.com/rickchristie/gent/output"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Keep complete entries so an image with native tool evidence or a signed assistant turn remains
// replayable. A policy can choose all such entries or just the latest without editing originals.
type imageCompaction struct{ latest bool }

func (policy imageCompaction) Compact(
	_ *gent.ExecutionContext,
	input *gent.CompactionInput,
) (
	*gent.HistoryUpdate,
	error,
) {
	var selected, images []gent.EntryId
	for _, entry := range input.Entries {
		// This custom policy keeps user input explicitly. The framework does not protect it.
		if entry.Origin == gent.RecordUserInput {
			selected = append(selected, entry.Id)
			continue
		}
		found := false
		for _, message := range entry.Messages {
			for _, part := range message.Parts {
				if _, ok := part.(gent.BinaryContent); ok {
					found = true
				}
			}
		}
		if found {
			if policy.latest {
				images = images[:0]
			}
			images = append(images, entry.Id)
		}
	}
	return &gent.HistoryUpdate{
		ExpectedDraftRevision: input.Revision,
		Active: append(
			selected,
			images...,
		),
	}, nil
}

type imageHistoryLoop struct {
	records  []*gent.HistoryEntry
	messages []*gent.ModelMessage
}

func (*imageHistoryLoop) Output() gent.Output[string] { return output.NewTextOutput() }

func (loop *imageHistoryLoop) Next(execCtx *gent.ExecutionContext) (*gent.AgentLoopResult, error) {
	if execCtx.Iteration() == 1 {
		for _, record := range loop.records {
			if err := execCtx.AppendRecord(record, true); err != nil {
				return nil, err
			}
		}
		return &gent.AgentLoopResult{Action: gent.LAContinue}, nil
	}
	history, err := execCtx.Scratchpad()
	if err != nil {
		return nil, err
	}
	messages, _ := agents.ScratchpadToMessages(history, execCtx.Iteration())
	loop.messages = append(append([]*gent.ModelMessage{}, execCtx.Instructions()...), messages...)
	return gent.AcceptOutput(execCtx, &gent.OutputResult[string]{Accepted: true, Value: "done",
		Content: []gent.ContentPart{gent.TextContent{Text: "done"}}}, nil)
}

func TestCustomCompactionRetainsImagesAndOriginalTask(t *testing.T) {
	type input struct{ latest bool }
	type expected struct{ indexes []int }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"all images", input{}, expected{[]int{0, 2}}},
		{"latest image", input{true}, expected{[]int{2}}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			task := &gent.Task{Instructions: []*gent.ModelMessage{
				gent.TextParts(gent.ChatMessageTypeSystem, "Keep the supplied image reference."),
			}, Messages: []*gent.ModelMessage{
				{Role: gent.ChatMessageTypeHuman, Parts: []gent.ContentPart{
					gent.TextContent{Text: "Compare images."},
					gent.BinaryContent{MIMEType: "image/png", Data: []byte{0, 1}}}},
			}}
			image := func(data byte) *gent.MessageContent {
				return &gent.MessageContent{Role: gent.ChatMessageTypeAI,
					Parts: []gent.ContentPart{gent.BinaryContent{MIMEType: "image/png", Data: []byte{data}}},
					Continuation: &gent.ProviderContinuation{Provider: "fixture",
						Items: []json.RawMessage{json.RawMessage(`{"signature":"keep"}`)}}}
			}
			records := []*gent.HistoryEntry{
				{Messages: []*gent.MessageContent{image(2)}},
				{Messages: []*gent.MessageContent{{Role: gent.ChatMessageTypeHuman,
					Parts: []gent.ContentPart{gent.TextContent{Text: "Temporary note."}}}}},
				{Messages: []*gent.MessageContent{
					{Role: gent.ChatMessageTypeAI, Parts: []gent.ContentPart{gent.ModelToolCallRequest{
						ID: "c1", Type: "function", FunctionCall: &gent.FunctionCall{
							Name: "draw", Arguments: `{}`}}}},
					{Role: gent.ChatMessageTypeTool, Parts: []gent.ContentPart{preparedTestReply("c1",
						"draw", "Created image.")}}, image(3)}},
			}
			loop := &imageHistoryLoop{records: records}
			store := &executionRecorder{codecs: gent.NewCodecRegistry()}
			runner := executor.NewRunner[string](loop, &gent.Config{Codecs: store.codecs, Recorder: store,
				CompactionTrigger:  tt.NewMockCompactionTrigger().WithShouldCompact(false, true),
				CompactionStrategy: imageCompaction{latest: test.input.latest}})
			ctx, err := runner.NewExecutionContext(context.Background(), "images")
			require.NoError(t, err)
			defer ctx.Release()
			value, result, err := runner.NewTurn(ctx, task)
			assert.NoError(t, err)
			assert.Equal(t, "done", value)
			assert.Equal(t, gent.TerminationSuccess, result.TerminationReason)
			want := append(append([]*gent.ModelMessage{}, task.Instructions...), task.Messages...)
			for _, index := range test.expected.indexes {
				for _, message := range records[index].Messages {
					want = append(want, &gent.ModelMessage{Role: message.Role,
						Parts: message.Parts, Continuation: message.Continuation})
				}
			}
			assert.Equal(t, want, loop.messages)
			for i, entry := range records {
				assert.Equal(t, entry.Messages, store.view.History.Entries[i+1].Messages)
			}
			assert.NoError(t, gent.ValidateBundle(*store.view, store.codecs))
			restoredResult, err := runner.Restore(context.Background(), store.latest(t, store.codecs))
			require.NoError(t, err)
			require.Equal(t, gent.RestoreReady, restoredResult.Status)
			restored := restoredResult.Context
			defer restored.Release()
			actual, err := restored.Task()
			assert.NoError(t, err)
			assert.Equal(t, task, actual)
			assert.Equal(t, ctx.History(), restored.History())
		})
	}
}
