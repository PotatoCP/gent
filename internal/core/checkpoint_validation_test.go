package gent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestReplayRejectsMalformedCheckpointWithoutMutation(t *testing.T) {
	type input struct{ change func(*Checkpoint) }
	type expected struct {
		result *ApplyStateResult
		err    error
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	invalid := func(detail string) expected {
		return expected{result: &ApplyStateResult{Status: ApplyInvalid, Detail: detail}}
	}
	tests := []testCase{
		{"missing turn", input{func(c *Checkpoint) { c.Contexts[0].TurnId = "" }},
			invalid("gent: checkpoint context, runtime, or turn identity is incomplete")},
		{"missing attempt", input{func(c *Checkpoint) { c.Contexts[0].AttemptId = "" }},
			invalid("gent: checkpoint context, runtime, or turn identity is incomplete")},
		{"future completed iteration", input{func(c *Checkpoint) {
			c.Contexts[0].CompletedIteration = c.Contexts[0].Iteration + 1
		}}, invalid("gent: invalid checkpoint iteration position")},
		{"closed paused turn", input{func(c *Checkpoint) { c.Contexts[0].TurnOpen = false }},
			invalid("gent: checkpoint phase and unfinished turn disagree")},
		{"terminal resume phase", input{func(c *Checkpoint) { c.ResumePhase = PhaseTerminal }},
			invalid(`gent: invalid saved resume phase "terminal"`)},
		{"missing task", input{func(c *Checkpoint) { c.Contexts[0].Task = EncodedValue{} }},
			invalid("gent: saved Task is missing or malformed")},
		{"non-system instruction", input{func(c *Checkpoint) {
			c.Contexts[0].Instructions = []*ModelMessage{TextParts(ChatMessageTypeHuman, "Wrong")}
		}}, invalid("gent: saved instruction prefix contains a non-system message")},
		{"missing LoopData", input{func(c *Checkpoint) { delete(c.Contexts[0].Extensions, "data") }},
			invalid("gent: saved LoopData is missing")},
		{"missing extension type", input{func(c *Checkpoint) {
			value := c.Contexts[0].Extensions["data"]
			value.Type = ""
			c.Contexts[0].Extensions["data"] = value
		}}, invalid(`gent: invalid saved component "data"`)},
		{"invalid pause reason", input{func(c *Checkpoint) {
			c.Outcome.Result.TerminationReason = TerminationSuccess
		}}, invalid(`gent: invalid saved pause reason "success"`)},
		{"missing limit details", input{func(c *Checkpoint) { c.Outcome.Result.ExceededLimit = nil }},
			invalid("gent: saved limit pause requires its effective limit")},
		{"wrong outcome turn", input{func(c *Checkpoint) { c.Outcome.Result.TurnId = "other" }},
			invalid("gent: checkpoint outcome does not match its turn and phase")},
	}
	codecs := NewCodecRegistry()
	runId, batches := recordedStateFixture(t, 1, codecs, nil)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, archive := range []bool{false, true} {
				state, view := NewRunState(runId), NewRunView(runId)
				apply := state.Apply
				capture := func() []byte { return encodeRunState(t, codecs, state) }
				if archive {
					apply = view.Apply
					capture = func() []byte {
						data, err := codecs.Marshal(view)
						assert.NoError(t, err)
						return data
					}
				}
				for _, data := range batches[:len(batches)-1] {
					var batch SaveBatch
					assert.NoError(t, codecs.Unmarshal(data, &batch))
					result, err := apply(&batch, codecs)
					assert.NoError(t, err)
					assert.Equal(t, &ApplyStateResult{Status: ApplyApplied}, result)
				}
				before := capture()
				var batch SaveBatch
				assert.NoError(t, codecs.Unmarshal(batches[len(batches)-1], &batch))
				assert.Equal(t, PhasePaused, batch.Checkpoint.Phase)
				// Change decoded storage input, not a live execution or accepted reducer.
				test.input.change(batch.Checkpoint)
				var err error
				batch.BatchId, err = BatchDigest(&batch, codecs)
				assert.NoError(t, err)
				batchBefore, err := codecs.Marshal(&batch)
				assert.NoError(t, err)
				result, err := apply(&batch, codecs)
				assert.Equal(t, test.expected.err, err)
				assert.Equal(t, test.expected.result, result)
				assert.Equal(t, before, capture())
				batchAfter, err := codecs.Marshal(&batch)
				assert.NoError(t, err)
				assert.Equal(t, batchBefore, batchAfter)
			}
		})
	}
}

type taskValidationData struct {
	BasicLoopData
	restoreCalls *int
}

func (data *taskValidationData) RestoreCheckpointState(value *EncodedValue) error {
	*data.restoreCalls++
	return data.BasicLoopData.RestoreCheckpointState(value)
}

func TestRestoreRejectsInvalidTaskBeforeCallbacks(t *testing.T) {
	type input struct{ task any }
	type expected struct {
		result *RestoreResult
		err    error
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	invalid := func(detail string) expected {
		return expected{result: &RestoreResult{Status: RestoreInvalid, Detail: detail}}
	}
	tests := []testCase{
		{"nil", input{nil}, invalid("gent: saved Task must contain a task, received <nil>")},
		{"nil task", input{(*Task)(nil)},
			invalid("gent: saved Task must contain a task, received *gent.Task")},
		{"wrong type", input{"prompt"},
			invalid("gent: saved Task must contain a task, received string")},
		{"no user input", input{&Task{}},
			invalid("gent: saved Task: Task.Messages must contain at least one user message")},
		{"nil message", input{&Task{Messages: []*ModelMessage{nil}}},
			invalid("gent: saved Task: Messages[0] is nil")},
		{"system in conversation", input{&Task{Messages: []*ModelMessage{
			TextParts(ChatMessageTypeSystem, "Policy"),
		}}}, invalid(`gent: saved Task: Messages[0] has invalid conversation role "system"`)},
		{"human instruction", input{&Task{Instructions: []*ModelMessage{
			TextParts(ChatMessageTypeHuman, "Policy"),
		}, Messages: NewTask(TextPart("Task")).Messages}},
			invalid("gent: saved Task: Instructions[0] must be a system message")},
	}
	codecs := NewCodecRegistry()
	runId, batches := recordedStateFixture(t, 1, codecs, nil)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var batch SaveBatch
			assert.NoError(t, codecs.Unmarshal(batches[0], &batch))
			var err error
			batch.Checkpoint.Contexts[0].Task, err = codecs.Encode(test.input.task)
			assert.NoError(t, err)
			batch.BatchId, err = BatchDigest(&batch, codecs)
			assert.NoError(t, err)
			state := NewRunState(runId)
			applied, err := state.Apply(&batch, codecs)
			if !assert.NoError(t, err) ||
				!assert.Equal(t, &ApplyStateResult{Status: ApplyApplied}, applied) {
				return
			}
			before := encodeRunState(t, codecs, state)
			restoreCalls := 0
			loop := &turnTestLoop{next: func(*ExecutionContext) (*AgentLoopResult, error) {
				t.Fatal("Restore must not execute the loop")
				return nil, nil
			}}
			runner := NewRunner[string](loop, &Config{NewLoopData: func() LoopData {
				return &taskValidationData{restoreCalls: &restoreCalls}
			}})
			result, err := runner.Restore(context.Background(), state)
			assert.Equal(t, test.expected.err, err)
			assert.Equal(t, test.expected.result, result)
			assert.Equal(t, 0, restoreCalls)
			assert.Equal(t, before, encodeRunState(t, codecs, state))
		})
	}
}
