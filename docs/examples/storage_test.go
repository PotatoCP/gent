package examples

import (
	"context"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/executor"
	"github.com/rickchristie/gent/output"
	"github.com/stretchr/testify/assert"
)

type pauseLoop struct{}

type firstCaptureStore struct {
	*MemoryStore
	first []byte
}

func (store *firstCaptureStore) Commit(ctx context.Context, batch *gent.SaveBatch,
	data, state []byte,
) error {
	if err := store.MemoryStore.Commit(ctx, batch, data, state); err != nil {
		return err
	}
	if store.first == nil && state != nil {
		store.first = state
	}
	return nil
}

func (*pauseLoop) Output() gent.Output[string] { return output.NewTextOutput() }

func (*pauseLoop) Next(execCtx *gent.ExecutionContext) (*gent.AgentLoopResult, error) {
	data := execCtx.Data().(*LoopData)
	if data.TurnFiles == 0 {
		data.Files++
		data.TurnFiles++
		if err := appendHistory(execCtx); err != nil {
			return nil, err
		}
		stopAttempt(execCtx)
		return &gent.AgentLoopResult{Action: gent.LAContinue}, nil
	}
	return (&LocalLoop{}).Next(execCtx)
}

func TestReplayExamples(t *testing.T) {
	type input struct{ capture bool }
	type expected struct {
		pause    gent.ExecutionStatus
		answer   string
		progress Progress
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"batches", input{false}, expected{gent.ExecutionPaused, "Ready.", Progress{1, 1}}},
		{"capture and tail", input{true}, expected{gent.ExecutionPaused, "Ready.", Progress{1, 1}}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			config, err := loopDataConfig()
			if !assert.NoError(t, err) {
				return
			}
			store := NewMemoryStore()
			base := BatchRecorder{Codecs: config.Codecs, Store: store}
			captures := &firstCaptureStore{MemoryStore: store}
			config.Recorder = &base
			if test.input.capture {
				base.Store = captures
				config.Recorder = &SnapshotRecorder{BatchRecorder: base, Every: 1}
			}
			accepted := &EventLog{}
			config.Subscribers = []*gent.SubscriberRegistration{{Subscriber: accepted}}
			runner := executor.NewRunner(&pauseLoop{}, config)
			execCtx, err := runner.NewExecutionContext(context.Background(), "saved")
			if !assert.NoError(t, err) {
				return
			}
			defer execCtx.Release()
			task := gent.NewTask(gent.TextPart("Check files."))
			answer, result, err := runner.NewTurn(execCtx, task)
			assert.NoError(t, err)
			assert.Equal(t, "", answer)
			assert.Equal(t, test.expected.pause, result.Status)
			originalTurn := execCtx.TurnId()
			saved := store.Read(execCtx.RunId())
			full, err := replayBatches(execCtx.RunId(), saved.Batches, config.Codecs)
			if !assert.NoError(t, err) {
				return
			}
			view, err := replayArchive(execCtx.RunId(), saved.Batches, config.Codecs)
			assert.NoError(t, err)
			assert.Equal(t, saved.Head, view.LastAppliedEventSequenceNumber)
			assert.Equal(t, view.Activity, accepted.Events)
			before, err := config.Codecs.MarshalRunState(full)
			assert.NoError(t, err)
			state := full
			if test.input.capture {
				if !assert.NotEmpty(t, captures.first) {
					return
				}
				loaded, err := config.Codecs.UnmarshalRunState(captures.first)
				assert.NoError(t, err)
				var tail [][]byte
				for _, data := range saved.Batches {
					var batch gent.SaveBatch
					assert.NoError(t, config.Codecs.Unmarshal(data, &batch))
					if batch.PreviousEventSequenceNumber >=
						loaded.State.LastAppliedEventSequenceNumber() {
						tail = append(tail, data)
					}
				}
				assert.NotEmpty(t, tail)
				state, err = replayTail(captures.first, tail, config.Codecs)
				assert.NoError(t, err)
				after, err := config.Codecs.MarshalRunState(state)
				assert.NoError(t, err)
				assert.Equal(t, before, after)
			}
			// The replacement owns fresh resources and the same accepted turn.
			execCtx.Release()
			restored, err := restoreContext(context.Background(), runner, state)
			if !assert.NoError(t, err) {
				return
			}
			defer restored.Release()
			assert.Equal(t, originalTurn, restored.TurnId())
			assert.Equal(t, test.expected.progress, restored.Data().(*LoopData).Progress)
			answer, result, err = continueTask(runner, restored)
			assert.NoError(t, err)
			assert.Equal(t, test.expected.answer, answer)
			assert.NoError(t, checkOutputResult(result))
			assert.Equal(t, test.expected.progress, restored.Data().(*LoopData).Progress)
			answer, result, err = continueTask(runner, restored)
			assert.NoError(t, err)
			assert.Equal(t, "", answer)
			assert.Equal(t, gent.ExecutionNoActiveTurn, result.Status)
			nextTask := gent.NewTask(gent.TextPart("Check more files."))
			_, result, err = runner.NewTurn(restored, nextTask)
			assert.NoError(t, err)
			assert.Equal(t, gent.ExecutionPaused, result.Status)
			assert.Equal(t, Progress{Files: 2, TurnFiles: 1}, restored.Data().(*LoopData).Progress)
			assert.NotEqual(t, originalTurn, restored.TurnId())
			// The exact retry is a no-op, including when its savepoint is old.
			var batch gent.SaveBatch
			assert.NoError(t, config.Codecs.Unmarshal(saved.Batches[0], &batch))
			storedBefore := store.Read(execCtx.RunId())
			assert.NoError(t, base.Save(context.Background(), &batch, nil))
			assert.Equal(t, storedBefore, store.Read(execCtx.RunId()))
			seq, err := traceArchive(view)
			assert.NoError(t, err)
			seq.Close()
		})
	}
}

func TestCodecsAndInvalidReplay(t *testing.T) {
	codecs, err := codecExample()
	assert.NoError(t, err)
	data, err := codecs.Marshal(Answer{"Ready", 2})
	assert.NoError(t, err)
	var answer Answer
	assert.NoError(t, codecs.Unmarshal(data, &answer))
	assert.Equal(t, Answer{"Ready", 2}, answer)
	custom, err := registerCodec()
	assert.NoError(t, err)
	encoded, err := custom.Encode(Progress{3, 1})
	assert.NoError(t, err)
	decoded, err := custom.Decode(encoded)
	assert.NoError(t, err)
	assert.Equal(t, Progress{3, 1}, decoded)
	state, err := replayBatches("", nil, codecs)
	assert.NoError(t, err)
	runner := executor.NewRunner(&LocalLoop{}, &gent.Config{Codecs: codecs})
	result, err := runner.Restore(context.Background(), state)
	assert.NoError(t, err)
	assert.Equal(t, gent.RestoreInvalid, result.Status)
	assert.Nil(t, result.Context)
}
