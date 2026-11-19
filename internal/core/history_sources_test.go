package gent

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func archiveSourceLoop(unknown bool) *turnTestLoop {
	return &turnTestLoop{next: func(execCtx *ExecutionContext) (*AgentLoopResult, error) {
		saved, found, err := execCtx.CheckpointState("test:original")
		if err != nil {
			return nil, err
		}
		var original EntryId
		if found {
			original = EntryId(saved.(string))
		} else {
			original = execCtx.ContextDraft().Active[0]
			if err := execCtx.SetCheckpointState("test:original", string(original)); err != nil {
				return nil, err
			}
		}
		if unknown {
			original = "unknown-archive-source"
		}
		entry, err := execCtx.NewHistoryEntry(&HistoryEntry{Origin: RecordCompactedModified,
			Replaces: []EntryId{original}, Messages: []*MessageContent{{Role: ChatMessageTypeHuman,
				Parts: []ContentPart{TextPart(fmt.Sprintf("Projection %d", execCtx.Iteration()))}}}})
		if err != nil {
			return nil, err
		}
		if err := execCtx.UpdateHistory(HistoryUpdate{
			ExpectedDraftRevision: execCtx.ContextDraft().Revision,
			AppendedEntries:       []*HistoryEntry{entry}, Active: []EntryId{entry.Id},
			Reason: ContextReplace,
		}); err != nil {
			return nil, err
		}
		if execCtx.Iteration() == 3 {
			return acceptTurnText(execCtx, "Restored source")
		}
		return &AgentLoopResult{Action: LAContinue}, nil
	}}
}

func TestArchiveReferencesSurviveCompactRestoration(t *testing.T) {
	type input struct {
		useRunStateSnapshot bool
		unknownSource       bool
	}
	type expected struct {
		value   string
		failure bool
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"full batches", input{}, expected{value: "Restored source"}},
		{"RunStateSnapshot plus tail", input{useRunStateSnapshot: true},
			expected{value: "Restored source"}},
		{"unknown source", input{unknownSource: true}, expected{failure: true}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			codecs := NewCodecRegistry()
			recorder := &runStateRecorder{codecs: codecs, capture: true}
			runner := NewRunner[string](archiveSourceLoop(false), &Config{Codecs: codecs,
				Recorder: recorder, Limits: []Limit{{Type: LimitExactKey, Key: SCIterations, MaxValue: 2}}})
			execCtx, err := runner.NewExecutionContext(context.Background(), t.Name())
			if !assert.NoError(t, err) {
				return
			}
			t.Cleanup(execCtx.Release)
			_, paused, err := runner.NewTurn(execCtx, NewTask(TextPart("Keep this original source.")))
			if !assert.NoError(t, err) || !assert.Equal(t, ExecutionPaused, paused.Status) {
				return
			}
			original, _, err := execCtx.CheckpointState("test:original")
			assert.NoError(t, err)
			_, retained := execCtx.History().Entry(EntryId(original.(string)))
			assert.False(t, retained, "the source must have left execution history")
			execCtx.Release()

			fresh := NewCodecRegistry()
			state := NewRunState(execCtx.RunId())
			start := 0
			if test.input.useRunStateSnapshot {
				start = len(recorder.states) - 1
				loaded, err := fresh.UnmarshalRunState(encodeRunState(t, codecs, recorder.states[start-1]))
				if !assert.NoError(t, err) || !assert.Equal(t, RunStateReady, loaded.Status) {
					return
				}
				state = loaded.State
			}
			for _, batch := range recorder.batches[start:] {
				applyRecordedState(t, state, fresh, batch)
			}
			assert.Equal(t, encodeRunState(t, codecs, recorder.states[len(recorder.states)-1]),
				encodeRunState(t, fresh, state))
			runner = NewRunner[string](archiveSourceLoop(test.input.unknownSource), &Config{Codecs: fresh})
			restored, err := runner.Restore(context.Background(), state)
			if !assert.NoError(t, err) || !assert.Equal(t, RestoreReady, restored.Status) {
				return
			}
			t.Cleanup(restored.Context.Release)
			value, result, err := runner.Continue(restored.Context)
			if test.expected.failure {
				assert.EqualError(t, err, fmt.Sprintf(
					"gent: Loop.Next *gent.turnTestLoop failed in run %q turn %q iteration 3: "+
						"gent: invalid replacement reference", execCtx.RunId(), paused.TurnId))
				assert.Nil(t, result)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, &ExecutionResult{Status: ExecutionFinished, TurnId: paused.TurnId,
					TerminationReason: TerminationSuccess,
					Output:            []ContentPart{TextPart("Restored source")}}, result)
			}
			assert.Equal(t, test.expected.value, value)
		})
	}
}
