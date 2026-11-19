package gent_test

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/agents/react"
	"github.com/rickchristie/gent/executor"
	"github.com/rickchristie/gent/output"
	"github.com/rickchristie/gent/schema"
	"github.com/stretchr/testify/assert"
)

type typedAnswer struct {
	ID     uint64   `json:"id"`
	Labels []string `json:"labels"`
}

type observedOutput[T any] struct {
	gent.Output[T]
	calls *atomic.Int64
}

func (out observedOutput[T]) Instructions() string { panic("instructions belong to the caller") }
func (out observedOutput[T]) Process(execCtx *gent.ExecutionContext, content []gent.ContentPart) (
	*gent.OutputResult[T], error,
) {
	out.calls.Add(1)
	return out.Output.Process(execCtx, content)
}
func typedJSONOutput[T any](
	t *testing.T, rule string, calls, validations *atomic.Int64,
) gent.Output[T] {
	t.Helper()
	out, err := output.NewJsonOutput[T](schema.Field("id").Description("An exact record ID."))
	if !assert.NoError(t, err) {
		t.FailNow()
	}
	out.SetCustomValidator(rule, func(*gent.ExecutionContext, T) (
		*gent.OutputValidationResult, error,
	) {
		validations.Add(1)
		return &gent.OutputValidationResult{Accepted: true}, nil
	})
	return observedOutput[T]{Output: out, calls: calls}
}

func TestTypedOutputReturnsAndRestores(t *testing.T) {
	t.Run("value", func(t *testing.T) {
		checkTypedOutput(t, typedAnswer{ID: 9007199254740993, Labels: []string{"saved"}})
	})
	t.Run("pointer", func(t *testing.T) {
		checkTypedOutput(t, &typedAnswer{ID: 9007199254740993, Labels: []string{"saved"}})
	})
}
func checkTypedOutput[T any](t *testing.T, expectedValue T) {
	t.Helper()
	const candidate = ` { "id":9007199254740993, "labels": ["saved"] } `
	const delivery = `{"id":9007199254740993,"labels":["saved"]}`
	var processed, validated atomic.Int64
	model := &contentModel{replies: correctionReplies(candidate)}
	task := &gent.Task{Instructions: []*gent.ModelMessage{
		gent.TextParts(gent.ChatMessageTypeSystem, "Return the record as JSON.")},
		Messages: []*gent.ModelMessage{gent.TextParts(gent.ChatMessageTypeHuman, "Read the record.")}}
	out := typedJSONOutput[T](t, "rules/v1", &processed, &validated)
	store := &recoveryStore{codecs: gent.NewCodecRegistry()}
	runner, execCtx := newTestExecution(t, context.Background(), react.NewWithOutput(model, out),
		&gent.Config{Codecs: store.codecs, Recorder: store})
	value, result, err := runner.NewTurn(execCtx, task)
	if !assert.NoError(t, err) {
		return
	}
	assert.Equal(t, expectedValue, value)
	expectedResult := &gent.ExecutionResult{Status: gent.ExecutionFinished, TurnId: execCtx.TurnId(),
		TerminationReason: gent.TerminationSuccess, Output: []gent.ContentPart{gent.TextPart(delivery)}}
	assert.Equal(t, expectedResult, result)
	assert.Equal(t, int64(1), processed.Load())
	assert.Equal(t, int64(1), validated.Load())
	assert.Equal(t, append(task.Instructions, task.Messages...), model.requests[0].Messages)
	type input struct{ rule string }
	type expected struct{ processed, validated int64 }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	for _, test := range []testCase{
		{"same validation", input{"rules/v1"}, expected{}},
		{"updated validation", input{"rules/v2"}, expected{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var processed, validated atomic.Int64
			freshModel := &contentModel{}
			freshOutput := typedJSONOutput[T](t, test.input.rule, &processed, &validated)
			freshRunner, fresh := restoreTestExecution(t, react.NewWithOutput(freshModel, freshOutput),
				nil, store.runStateSnapshot)
			value, result, err := freshRunner.GetLastTurnResult(fresh)
			assert.NoError(t, err)
			assert.Equal(t, expectedValue, value)
			assert.Equal(t, expectedResult, result)
			assert.Equal(t, test.expected, expected{processed.Load(), validated.Load()})
			assert.Empty(t, freshModel.requests)
		})
	}
	// A changed result type cannot decode an old value as if it were the new contract.
	incompatible := executor.NewRunner(
		react.NewAgent(
			&contentModel{},
		),
		&gent.Config{
			Codecs: store.codecs,
		},
	)
	loaded, err := store.codecs.UnmarshalRunState(store.runStateSnapshot)
	if !assert.NoError(t, err) {
		return
	}
	restored, err := incompatible.Restore(context.Background(), loaded.State)
	assert.NoError(t, err)
	assert.Equal(t, gent.RestoreIncompatible, restored.Status)
	assert.Nil(t, restored.Context)
}

// This strategy deliberately discards every entry. The core must preserve the pending correction
// even when a custom strategy does not know how the agent grouped its validation feedback.
type discardHistory struct{}

func (discardHistory) Compact(
	_ *gent.ExecutionContext,
	input *gent.CompactionInput,
) (
	*gent.HistoryUpdate,
	error,
) {
	return &gent.HistoryUpdate{ExpectedDraftRevision: input.Revision}, nil
}

type alwaysCompact struct{}

func (alwaysCompact) ShouldCompact(*gent.ExecutionContext) (bool, error) { return true, nil }
func (alwaysCompact) NotifyCompacted(*gent.ExecutionContext) error       { return nil }

type correctionStore struct {
	view             gent.RunView
	cancel           context.CancelFunc
	codecs           *gent.CodecRegistry
	runStateSnapshot []byte
}

func (store *correctionStore) Save(
	_ context.Context, batch *gent.SaveBatch,
	captureRunStateSnapshot func() (*gent.RunState, error),
) error {
	if store.view.RunId == "" {
		store.view = gent.RunView{SchemaVersion: gent.RunSchemaVersion, RunId: batch.RunId}
	}
	if err := applyTestBatch(&store.view, batch, store.codecs); err != nil {
		return err
	}
	state, err := captureRunStateSnapshot()
	if err != nil {
		return err
	}
	store.runStateSnapshot, err = store.codecs.MarshalRunState(state)
	if err != nil {
		return err
	}
	if batch.Checkpoint != nil && batch.Checkpoint.Phase == gent.PhaseIterationCompleted &&
		batch.Checkpoint.Contexts[0].Iteration == 1 && store.cancel != nil {
		store.cancel()
		store.cancel = nil
	}
	return nil
}

func correctionOutput() *output.TextOutput {
	return output.NewTextOutput().SetCustomValidator("rules", func(
		_ *gent.ExecutionContext, value string,
	) (
		*gent.OutputValidationResult, error,
	) {
		if value == "ok" {
			return &gent.OutputValidationResult{Accepted: true}, nil
		}
		return &gent.OutputValidationResult{Steering: []gent.ContentPart{
			gent.TextContent{Text: "Replace " + value + " with ok."},
			gent.BinaryContent{MIMEType: "image/png", Data: []byte{1, 2, 3}},
		}}, nil
	})
}

func correctionReplies(values ...string) []contentReply {
	var replies []contentReply
	for _, value := range values {
		replies = append(replies, contentReply{choice: recoveryAnswerChoice(value)})
	}
	return replies
}

func TestOutputCorrectionSurvivesCompactionAndRestore(t *testing.T) {
	type input struct{ restore bool }
	type expected struct {
		calls      int
		rejections int64
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"one worker", input{}, expected{3, 2}},
		{"replacement worker", input{true}, expected{3, 2}},
	}
	for _, test := range cases {
		t.Run(
			test.name,
			func(t *testing.T) {
				worker, cancel := context.WithCancel(context.Background())
				defer cancel()
				store := &correctionStore{codecs: gent.NewCodecRegistry()}
				if test.input.restore {
					store.cancel = cancel
				}
				task := &gent.Task{Instructions: []*gent.ModelMessage{
					gent.TextParts(gent.ChatMessageTypeSystem, "Check all answers.")},
					Messages: []*gent.ModelMessage{
						gent.TextParts(gent.ChatMessageTypeHuman, "Answer now."),
					}}
				model := &contentModel{replies: correctionReplies("bad", "wrong", "ok")}
				config := &gent.Config{Codecs: store.codecs, Recorder: store,
					CompactionTrigger: alwaysCompact{}, CompactionStrategy: discardHistory{}}
				agent := react.NewWithOutput(model, correctionOutput())
				runner, execCtx := newTestExecution(t, worker, agent, config)
				answer, result, err := runner.NewTurn(execCtx, task)
				requests := model.requests
				if test.input.restore {
					if !assert.ErrorIs(t, err, context.Canceled) {
						return
					}
					assert.Equal(t, "", answer)
					assert.Equal(t, gent.PhaseIterationCompleted, store.view.Checkpoint.Phase)
					replacement := &contentModel{replies: correctionReplies("wrong", "ok")}
					agent := react.NewWithOutput(replacement, correctionOutput())
					freshRunner, fresh := restoreTestExecution(t, agent, config, store.runStateSnapshot)
					answer, result, err = freshRunner.Continue(fresh)
					requests = append(requests, replacement.requests...)
				}
				if !assert.NoError(t, err) {
					return
				}
				assert.Equal(t, "ok", answer)
				assert.Equal(
					t,
					&gent.ExecutionResult{
						Status:            gent.ExecutionFinished,
						TurnId:            execCtx.TurnId(),
						TerminationReason: gent.TerminationSuccess,
						Output:            []gent.ContentPart{gent.TextContent{Text: "ok"}},
					},
					result,
				)
				if !assert.Len(t, requests, test.expected.calls) {
					return
				}
				assert.Equal(t, task.Instructions, requests[0].Messages)
				for i, candidate := range []string{"bad", "wrong"} {
					want := append([]*gent.ModelMessage(nil), task.Instructions...)
					want = append(
						want,
						&gent.ModelMessage{
							Role: gent.ChatMessageTypeAI,
							Parts: []gent.ContentPart{
								&gent.AssistantContent{

									MessageId: requests[i+1].Messages[1].Parts[0].(*gent.AssistantContent).MessageId,
									Type:      gent.AssistantContentFinalAnswerCandidate,
									Content:   []gent.ContentPart{gent.TextPart(candidate)},
								},
							},
						},
						&gent.ModelMessage{
							Role: gent.ChatMessageTypeHuman, Parts: []gent.ContentPart{
								gent.TextContent{Text: "Replace " + candidate + " with ok."},
								gent.BinaryContent{MIMEType: "image/png", Data: []byte{1, 2, 3}},
							}},
					)
					assert.Equal(t, want, requests[i+1].Messages)
				}
				var candidates []string
				for _, entry := range store.view.History.Entries {
					for _, message := range entry.Messages {
						if message.Role == gent.ChatMessageTypeAI {
							candidates = append(candidates, gent.ContentText(message.Parts))
						}
					}
				}
				assert.Equal(t, []string{"bad", "wrong", "ok"}, candidates)
				_, fresh := restoreTestExecution(t, react.NewWithOutput(&contentModel{}, correctionOutput()),
					config, store.runStateSnapshot)
				assert.Equal(t, test.expected.rejections, fresh.Stats().GetCounter(gent.SCOutputRejectedTotal))
				assert.Equal(t, test.expected.rejections,
					fresh.Stats().GetCounter(gent.SCOutputCustomRejectedTotal))
				assert.Equal(t, test.expected.rejections,
					fresh.Stats().GetCounter(gent.SCOutputCustomRejectedBy+"rules"))
				assert.Equal(t, float64(0), fresh.Stats().GetGauge(gent.SGOutputRejectedConsecutive))
				assert.Equal(t, float64(0), fresh.Stats().GetGauge(gent.SGOutputCustomRejectedConsecutive))
				savedTask, err := fresh.Task()
				assert.NoError(t, err)
				assert.Equal(t, task, savedTask)
				snapshot, err := fresh.CompactionInput()
				assert.NoError(t, err)
				assert.Empty(t, snapshot.Required)
			},
		)
	}
}

type decisionOutput struct {
	result *gent.OutputResult[string]
	err    error
}

func (decisionOutput) Instructions() string                   { return "" }
func (decisionOutput) Configure(*gent.ModelCallRequest) error { return nil }
func (out decisionOutput) Process(*gent.ExecutionContext, []gent.ContentPart) (
	*gent.OutputResult[string], error,
) {
	return out.result, out.err
}

func TestOutputInvalidDecisionsStopTheLoop(t *testing.T) {
	type input struct {
		result *gent.OutputResult[string]
		err    error
	}
	type expected struct{ message string }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"no result", input{}, expected{"gent: output returned no result"}},
		{"blank steering", input{result: &gent.OutputResult[string]{Steering: []gent.ContentPart{
			gent.TextContent{Text: " "}}}}, expected{"gent: rejected output requires useful steering"}},
		{"acceptance with steering", input{result: &gent.OutputResult[string]{Accepted: true,
			Value: "unsafe", Steering: []gent.ContentPart{gent.TextContent{Text: "retry"}}}},
			expected{"gent: accepted output contains steering"}},
		{"error ignores acceptance", input{result: &gent.OutputResult[string]{Accepted: true,
			Value: "unsafe"}, err: errors.New("processing failed")}, expected{"processing failed"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			model := &contentModel{replies: correctionReplies("candidate")}
			loop := react.NewWithOutput(model, decisionOutput{test.input.result, test.input.err})
			runner, execCtx := newTestExecution(t, context.Background(), loop, nil)
			answer, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Answer.")))
			assert.EqualError(t, err, fmt.Sprintf(
				"gent: Loop.Next %T failed in run %q turn %q iteration 1: %s",
				loop, execCtx.RunId(), execCtx.TurnId(), test.expected.message))
			assert.Equal(t, "", answer)
			assert.Nil(t, result)
			assert.Len(t, model.requests, 1)
			assert.Equal(t, int64(0), execCtx.Stats().GetCounter(gent.SCOutputRejectedTotal))
		})
	}
}

type validationLoop struct {
	checks []*output.TextOutput
	next   int
}

func (*validationLoop) Output() gent.Output[string] { return output.NewTextOutput() }
func (loop *validationLoop) Next(execCtx *gent.ExecutionContext) (*gent.AgentLoopResult, error) {
	check := loop.checks[loop.next]
	loop.next++
	_, err := check.Process(execCtx, []gent.ContentPart{gent.TextContent{Text: "candidate"}})
	if err != nil {
		return nil, err
	}
	return &gent.AgentLoopResult{
		Action: gent.LAContinue,
	}, nil
}

func TestOutputCustomValidationPrefixLimit(t *testing.T) {
	loop := &validationLoop{}
	for _, name := range []string{"alpha", "beta", "beta", "beta"} {
		loop.checks = append(loop.checks, output.NewTextOutput().SetCustomValidator(name,
			func(*gent.ExecutionContext, string) (*gent.OutputValidationResult, error) {
				return &gent.OutputValidationResult{Steering: []gent.ContentPart{
					gent.TextContent{Text: "Return an approved result."}}}, nil
			}))
	}
	limit := gent.Limit{Type: gent.LimitKeyPrefix, Key: gent.SCOutputCustomRejectedBy, MaxValue: 2,
		Scope: gent.LimitTurn}
	store := &recoveryStore{codecs: gent.NewCodecRegistry()}
	runner, execCtx := newTestExecution(
		t,
		context.Background(),
		loop,
		&gent.Config{
			Limits: []gent.Limit{
				limit,
			},
			Recorder: store,
			Codecs:   store.codecs,
		},
	)
	value, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Check candidates.")))
	assert.NoError(t, err)
	assert.Equal(t, "", value)
	assert.Equal(t, gent.ExecutionPaused, result.Status)
	assert.Equal(t, gent.TerminationLimitExceeded, result.TerminationReason)
	assert.Equal(
		t,
		&gent.LimitExceeded{
			Limit:        &limit,
			MatchedKey:   gent.SCOutputCustomRejectedBy + "beta",
			CurrentValue: 3,
		},
		result.ExceededLimit,
	)
	assert.Equal(t, 4, execCtx.Iteration())
	assert.Equal(t, int64(1), execCtx.Stats().GetCounter(gent.SCOutputCustomRejectedBy+"alpha"))
	assert.Equal(t, int64(3), execCtx.Stats().GetCounter(gent.SCOutputCustomRejectedBy+"beta"))
	var exceeded []gent.StatKey
	for _, event := range store.view.Activity {
		if event, ok := event.Payload.(*gent.LimitExceededEvent); ok {
			exceeded = append(exceeded, event.MatchedKey)
		}
	}
	assert.Equal(t, []gent.StatKey{gent.SCOutputCustomRejectedBy + "beta"}, exceeded)
}

func TestNullableTypedOutputTerminalRestore(t *testing.T) {
	out, err := output.NewSchemaOutput[*string](schema.Nullable(schema.String("A label").Schema()))
	if !assert.NoError(t, err) {
		return
	}
	model := &contentModel{replies: correctionReplies("null")}
	store := &recoveryStore{codecs: gent.NewCodecRegistry()}
	runner, execCtx := newTestExecution(t, context.Background(), react.NewWithOutput(model, out),
		&gent.Config{Codecs: store.codecs, Recorder: store})
	value, result, err := runner.NewTurn(
		execCtx,
		gent.NewTask(
			gent.TextPart(
				"Return a nullable label.",
			),
		),
	)
	if !assert.NoError(t, err) {
		return
	}
	assert.Nil(t, value)
	assert.Equal(
		t,
		&gent.ExecutionResult{
			Status:            gent.ExecutionFinished,
			TurnId:            execCtx.TurnId(),
			TerminationReason: gent.TerminationSuccess,
			Output: []gent.ContentPart{
				gent.TextPart(
					"null",
				),
			},
		},
		result,
	)
	freshModel := &contentModel{}
	freshRunner, fresh := restoreTestExecution(
		t,
		react.NewWithOutput(
			freshModel,
			out,
		),
		nil,
		store.runStateSnapshot,
	)
	restored, metadata, err := freshRunner.GetLastTurnResult(fresh)
	assert.NoError(t, err)
	assert.Nil(t, restored)
	assert.Equal(t, result, metadata)
	assert.Empty(t, freshModel.requests)
}
