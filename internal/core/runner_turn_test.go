package gent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

type turnTestData struct {
	Runs   int
	Turns  int
	Resets int
}

func (*turnTestData) CheckpointType() string { return "test:turn_data" }
func (*turnTestData) CheckpointVersion() int { return 1 }
func (data *turnTestData) ResetTurn()        { data.Turns = 0; data.Resets++ }
func (data *turnTestData) SaveCheckpointState() (*EncodedValue, error) {
	value, err := json.Marshal(data)
	return &EncodedValue{Type: data.CheckpointType(), Version: 1, Data: value}, err
}
func (data *turnTestData) RestoreCheckpointState(value *EncodedValue) error {
	return json.Unmarshal(value.Data, data)
}

type turnTestOutput struct{}

func (turnTestOutput) Instructions() string              { return "" }
func (turnTestOutput) Configure(*ModelCallRequest) error { return nil }
func (turnTestOutput) Process(*ExecutionContext, []ContentPart) (*OutputResult[string], error) {
	return nil, errors.New("the custom test Loop supplies its accepted result")
}

type turnTestLoop struct {
	next func(*ExecutionContext) (*AgentLoopResult, error)
}

func (loop *turnTestLoop) Output() Output[string] { return turnTestOutput{} }
func (loop *turnTestLoop) Next(execCtx *ExecutionContext) (*AgentLoopResult, error) {
	return loop.next(execCtx)
}

func acceptTurnText(execCtx *ExecutionContext, text string) (*AgentLoopResult, error) {
	parts := []ContentPart{TextPart(text)}
	if err := execCtx.AppendRecord(&HistoryEntry{Messages: []*MessageContent{
		{Role: ChatMessageTypeAI, Parts: parts},
	}}, true); err != nil {
		return nil, err
	}
	return AcceptOutput(
		execCtx,
		&OutputResult[string]{
			Accepted: true,
			Value:    text,
			Content:  parts,
		},
		nil,
	)
}

func TestRunnerTurnsRestoreInstructionsAndCustomData(t *testing.T) {
	type input struct{ instructions []*ModelMessage }
	type expected struct{ instructions []*ModelMessage }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	firstPrefix := []*ModelMessage{TextParts(ChatMessageTypeSystem, "Keep exact task notes.")}
	secondPrefix := []*ModelMessage{TextParts(ChatMessageTypeSystem, "Write concise answers.")}
	cases := []testCase{
		{"inherit", input{}, expected{firstPrefix}},
		{"replace", input{secondPrefix}, expected{secondPrefix}},
		{"clear", input{[]*ModelMessage{}}, expected{[]*ModelMessage{}}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			codecs := NewCodecRegistry()
			recorder := &runStateRecorder{codecs: codecs, capture: true}
			calls := 0
			loop := &turnTestLoop{next: func(execCtx *ExecutionContext) (*AgentLoopResult, error) {
				calls++
				data := execCtx.Data().(*turnTestData)
				data.Runs++
				data.Turns++
				execCtx.Stats().IncrCounter("test:work", 3)
				task, err := execCtx.Task()
				if err != nil {
					return nil, err
				}
				return acceptTurnText(execCtx, "Answer: "+ContentText(task.Messages[0].Parts))
			}}
			config := &Config{Codecs: codecs, Recorder: recorder,
				NewLoopData: func() LoopData { return &turnTestData{} }}
			runner := NewRunner[string](loop, config)
			execCtx, err := runner.NewExecutionContext(context.Background(), "conversation")
			if !assert.NoError(t, err) {
				return
			}
			defer execCtx.Release()
			runId := execCtx.RunId()
			value, result, err := runner.NewTurn(execCtx, &Task{Instructions: firstPrefix,
				Messages: NewTask(TextPart("First task")).Messages})
			if !assert.NoError(t, err) {
				return
			}
			firstTurn := execCtx.TurnId()
			assert.Equal(t, "Answer: First task", value)
			assert.Equal(t, &ExecutionResult{Status: ExecutionFinished, TurnId: firstTurn,
				TerminationReason: TerminationSuccess,
				Output:            []ContentPart{TextPart("Answer: First task")}}, result)
			value, result, err = runner.NewTurn(execCtx, &Task{Instructions: tt.input.instructions,
				Messages: NewTask(TextPart("Second task")).Messages})
			if !assert.NoError(t, err) {
				return
			}
			assert.Equal(t, "Answer: Second task", value)
			assert.Equal(t, &ExecutionResult{Status: ExecutionFinished, TurnId: execCtx.TurnId(),
				TerminationReason: TerminationSuccess,
				Output:            []ContentPart{TextPart("Answer: Second task")}}, result)
			assert.NotEqual(t, firstTurn, execCtx.TurnId())
			assert.Equal(t, runId, execCtx.RunId())
			assert.Equal(t, tt.expected.instructions, execCtx.Instructions())
			assert.Equal(t, &turnTestData{Runs: 2, Turns: 1, Resets: 2}, execCtx.Data())
			assert.Equal(t, int64(6), execCtx.Stats().GetCounter("test:work"))
			assert.Equal(t, int64(3), execCtx.Stats().GetTurnCounter("test:work"))
			full := NewRunState(runId)
			for i, batch := range recorder.batches {
				applyRecordedState(t, full, codecs, batch)
				assert.Equal(t, encodeRunState(t, codecs, full), encodeRunState(t, codecs, recorder.states[i]))
			}
			loaded, err := codecs.UnmarshalRunState(encodeRunState(t, codecs, recorder.states[1]))
			if !assert.NoError(t, err) || !assert.Equal(t, RunStateReady, loaded.Status) {
				return
			}
			for _, batch := range recorder.batches[2:] {
				applyRecordedState(t, loaded.State, codecs, batch)
			}
			assert.Equal(t, encodeRunState(t, codecs, full), encodeRunState(t, codecs, loaded.State))
			restored, err := runner.Restore(context.Background(), loaded.State)
			if !assert.NoError(t, err) || !assert.Equal(t, RestoreReady, restored.Status) {
				return
			}
			defer restored.Context.Release()
			last, lastResult, err := runner.GetLastTurnResult(restored.Context)
			assert.NoError(t, err)
			assert.Equal(t, value, last)
			assert.Equal(t, result, lastResult)
			assert.Equal(t, 2, calls)
			assert.Equal(t, &turnTestData{Runs: 2, Turns: 1, Resets: 2}, restored.Context.Data())
			value, result, err = runner.Continue(restored.Context)
			assert.NoError(t, err)
			assert.Equal(t, "", value)
			assert.Equal(t, &ExecutionResult{Status: ExecutionNoActiveTurn}, result)
			value, _, err = runner.NewTurn(restored.Context, NewTask(TextPart("Third task")))
			assert.NoError(t, err)
			assert.Equal(t, "Answer: Third task", value)
			assert.Equal(t, &turnTestData{Runs: 3, Turns: 1, Resets: 3}, restored.Context.Data())
		})
	}
}

func TestRunnerStopBusyAndReuse(t *testing.T) {
	type input struct{ nextTask bool }
	type expected struct {
		oldReason TerminationReason
		calls     int
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"continue", input{}, expected{TerminationSuccess, 2}},
		{"replace", input{true}, expected{TerminationCanceled, 2}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			started := make(chan struct{})
			calls := 0
			codecs := NewCodecRegistry()
			recorder := &runStateRecorder{codecs: codecs, capture: true}
			loop := &turnTestLoop{next: func(execCtx *ExecutionContext) (*AgentLoopResult, error) {
				calls++
				if calls == 1 {
					close(started)
					<-execCtx.Context().Done()
					return nil, execCtx.Context().Err()
				}
				if tt.input.nextTask {
					_, old, err := execCtx.runner.(*Runner[string]).GetLastTurnResult(execCtx)
					if err != nil {
						return nil, err
					}
					assert.Equal(t, tt.expected.oldReason, old.TerminationReason)
				}
				return acceptTurnText(execCtx, "Done")
			}}
			runner := NewRunner[string](loop, &Config{Recorder: recorder, Codecs: codecs})
			execCtx, err := runner.NewExecutionContext(context.Background(), "stop")
			if !assert.NoError(t, err) {
				return
			}
			defer execCtx.Release()
			type response struct {
				value  string
				result *ExecutionResult
				err    error
			}
			done := make(chan response, 1)
			go func() {
				value, result, err := runner.NewTurn(execCtx, NewTask(TextPart("Initial task")))
				done <- response{value, result, err}
			}()
			<-started
			turnId := execCtx.TurnId()
			value, result, err := runner.NewTurn(execCtx, NewTask(TextPart("Busy task")))
			assert.Equal(t, response{"", &ExecutionResult{Status: ExecutionBusy}, nil},
				response{value, result, err})
			execCtx.Stop()
			execCtx.Stop()
			stopped := <-done
			assert.Equal(t, response{"", &ExecutionResult{Status: ExecutionPaused, TurnId: turnId,
				TerminationReason: TerminationStopped}, nil}, stopped)
			oldCtx := execCtx.Context()
			execCtx.Stop()
			if tt.input.nextTask {
				value, result, err = runner.NewTurn(execCtx, NewTask(TextPart("Next task")))
				assert.NotEqual(t, turnId, execCtx.TurnId())
			} else {
				value, result, err = runner.Continue(execCtx)
				assert.Equal(t, turnId, execCtx.TurnId())
			}
			assert.Equal(t, response{"Done", &ExecutionResult{Status: ExecutionFinished,
				TurnId: execCtx.TurnId(), TerminationReason: TerminationSuccess,
				Output: []ContentPart{TextPart("Done")}}, nil}, response{value, result, err})
			assert.Equal(t, tt.expected.calls, calls)
			assert.ErrorIs(t, oldCtx.Err(), context.Canceled)
			full := NewRunState(execCtx.RunId())
			for i, batch := range recorder.batches {
				applyRecordedState(t, full, codecs, batch)
				assert.Equal(t, encodeRunState(t, codecs, full),
					encodeRunState(t, codecs, recorder.states[i]), fmt.Sprintf("batch %d", i))
			}
		})
	}
}
