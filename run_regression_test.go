package gent_test

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/agents/react"
	"github.com/rickchristie/gent/internal/tt"
	"github.com/rickchristie/gent/trace"
	"github.com/stretchr/testify/assert"
)

func TestRunRestoresAcceptedPrefixAfterFailedTerminalSave(t *testing.T) {
	type input struct{ responseOnly bool }
	type expected struct {
		answer string
		calls  int
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	for _, test := range []testCase{
		{"streamed response", input{}, expected{"replacement", 1}},
		{"response without chunks", input{true}, expected{"replacement", 1}},
	} {
		t.Run(
			test.name,
			func(t *testing.T) {
				store := newCheckpointStore()
				failure := errors.New("terminal save unavailable")
				store.before = func(batch *gent.SaveBatch) error {
					if batch.Checkpoint.Phase == gent.PhaseTerminal {
						return failure
					}
					return nil
				}
				original := &savedReplyModel{
					responses: []string{
						"original",
					},
					responseOnly: test.input.responseOnly,
				}
				runner, execCtx := newTestExecution(t, context.Background(), react.NewAgent(original),
					&gent.Config{Codecs: store.codecs, Recorder: store})
				_, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Review.")))
				assert.ErrorIs(t, err, failure)
				assert.Nil(t, result)
				assert.Equal(t, gent.PhaseIterationReady, store.view.Checkpoint.Phase)
				// Model evidence in the rejected terminal batch is not an accepted result.
				fresh := &savedReplyModel{
					responses: []string{
						"replacement",
					},
					responseOnly: test.input.responseOnly,
				}
				freshRunner, restored := restoreTestExecution(
					t,
					react.NewAgent(
						fresh,
					),
					nil,
					store.runStateSnapshot,
				)
				answer, result, err := freshRunner.Continue(restored)
				assert.NoError(t, err)
				assert.Equal(t, gent.ExecutionFinished, result.Status)
				assert.Equal(t, test.expected, expected{answer, fresh.calls})
			},
		)
	}
}

func TestRunCustomSettingsRecovery(t *testing.T) {
	type input struct{ maxTokens int }
	type expected struct {
		answer           string
		calls, maxTokens int
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	for _, test := range []testCase{
		{"same settings", input{128}, expected{"Done.", 1, 128}},
		{"changed settings", input{256}, expected{"Done.", 1, 256}},
	} {
		t.Run(
			test.name,
			func(t *testing.T) {
				owner, cancel := context.WithCancel(context.Background())
				defer cancel()
				store := &recoveryStore{
					codecs: gent.NewCodecRegistry(),
					phase:  gent.PhaseInitial,
					cancel: cancel,
				}
				model := &contentModel{}
				agent := react.NewAgent(model).WithCallOptions(gent.WithMaxTokens(128))
				runner, execCtx := newTestExecution(
					t,
					owner,
					agent,
					&gent.Config{
						Codecs:   store.codecs,
						Recorder: store,
					},
				)
				_, _, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Read the saved task.")))
				assert.ErrorIs(t, err, context.Canceled)
				assert.Empty(t, model.requests)
				fresh := &contentModel{replies: correctionReplies("Done.")}
				freshAgent := react.NewAgent(fresh).WithCallOptions(gent.WithMaxTokens(test.input.maxTokens))
				freshRunner, restored := restoreTestExecution(t, freshAgent, nil, store.runStateSnapshot)
				answer, _, err := freshRunner.Continue(restored)
				if !assert.NoError(t, err) {
					return
				}
				assert.Equal(
					t,
					test.expected,
					expected{
						answer,
						len(
							fresh.requests,
						),
						fresh.requests[0].Options.MaxTokens,
					},
				)
			},
		)
	}
}

func TestRunTraceOutputPolicy(t *testing.T) {
	type input struct{ config trace.Config }
	type expected struct {
		runOutput, iterationOutput any
		err                        error
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{

		{"excluded", input{trace.Config{}}, expected{}},
		{
			"redacted",
			input{
				trace.Config{
					IncludeRunOutput: true,
					Redactor: trace.RedactorFuncs{

						RunOutput: func(
							[]gent.ContentPart,
						) any {
							return "REDACTED"
						},
					},
				},
			},
			expected{
				"REDACTED",
				"REDACTED",
				nil,
			},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			seq, err := trace.NewSequencer("", test.input.config)
			if !assert.NoError(t, err) {
				return
			}
			defer seq.Close()
			live, unsubscribe := seq.Subscribe()
			defer unsubscribe()
			store := newCheckpointStore()
			model := &savedReplyModel{responses: []string{"PRIVATE ANSWER"}}
			runner, execCtx := newTestExecution(t, context.Background(), react.NewAgent(model),
				&gent.Config{Codecs: store.codecs, Recorder: store,
					Subscribers: []*gent.SubscriberRegistration{{Subscriber: seq}}})
			_, _, err = runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Review.")))
			saved := seq.Snapshot()
			if !assert.Len(t, saved.Iterations, 1) {
				return
			}
			var iterationOutput any
			for _, event := range saved.RecentEvents {
				if event.Type == trace.EventTypeIterationFinished {
					iterationOutput = event.Payload.(map[string]any)["result"]
				}
			}
			assert.Equal(t, test.expected, expected{saved.Result.Output, iterationOutput, err})
			seq.Close()
			var events []*trace.Event
			for event := range live {
				events = append(events, event)
			}
			assert.Equal(t, saved.RecentEvents, events)
			recent, err := seq.EventsAfter(0)
			assert.NoError(t, err)
			assert.Equal(t, events, recent)
			restored, err := trace.RestoreSequencer(store.view, test.input.config)
			if !assert.NoError(t, err) {
				return
			}
			defer restored.Close()
			assert.Equal(t, saved, restored.Snapshot())
			recent, err = restored.EventsAfter(0)
			assert.NoError(t, err)
			assert.Equal(t, events, recent)
		})
	}
}

func TestRunConcurrentHistoryAndIteration(t *testing.T) {
	execCtx := tt.NewContext(t, &gent.Config{})
	var workers sync.WaitGroup
	workers.Add(2)
	failures := make(chan error, 1)
	go func() {
		defer workers.Done()
		for range 10000 {
			execCtx.IncrementIteration()
			runtime.Gosched()
		}
	}()
	go func() {
		defer workers.Done()
		for range 100 {
			if err := execCtx.AppendRecord(&gent.HistoryEntry{}, true); err != nil {
				failures <- err
				return
			}
		}
	}()
	workers.Wait()
	close(failures)
	for err := range failures {
		assert.NoError(t, err)
	}
	assert.Equal(t, 10000, execCtx.Iteration())
	input, err := execCtx.CompactionInput()
	assert.NoError(t, err)
	assert.Len(t, input.Entries, 100)
}
