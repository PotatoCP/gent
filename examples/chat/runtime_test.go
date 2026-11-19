package main

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/rickchristie/gent/integrationtest/airline"
	"github.com/rickchristie/gent/toolchain"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rickchristie/gent"
	"github.com/stretchr/testify/assert"
)

func testApplication(t *testing.T, delay time.Duration) (*application, *atomic.Int64) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	app := newApplication(ctx, func() gent.Model { return &demoModel{delay} },
		"demo", demoEmbedder{})
	t.Cleanup(func() { cancel(); app.wait.Wait() })
	var calls atomic.Int64
	factory := app.newSession
	app.newSession = func(id scenarioId) (*scenarioSession, error) {
		session, err := factory(id)
		if err != nil {
			return nil, err
		}
		chain := &countingRegistry{ToolChain: toolchain.New(), calls: &calls}
		session.config.RegisterTools(chain)
		session.tools = chain
		return session, nil
	}
	return app, &calls
}

type countedTool struct {
	gent.Tool[airline.GetCustomerInfoInput]
	calls *atomic.Int64
}

func (tool *countedTool) Call(ctx context.Context, input airline.GetCustomerInfoInput) (
	*gent.ToolExecutionResponse, error,
) {
	tool.calls.Add(1)
	return tool.Tool.Call(ctx, input)
}

type countingRegistry struct {
	gent.ToolChain
	calls *atomic.Int64
}

func (chain *countingRegistry) RegisterTool(value any) gent.ToolChain {
	if tool, ok := value.(gent.Tool[airline.GetCustomerInfoInput]); ok {
		value = &countedTool{Tool: tool, calls: chain.calls}
	}
	chain.ToolChain.RegisterTool(value)
	return chain
}

func executeAction(t *testing.T, app *application, input *actionRequest) *browserState {
	t.Helper()
	runId, status, err := app.start(input)
	if !assert.NoError(t, err) || !assert.Equal(t, 202, status) {
		t.FailNow()
	}
	app.wait.Wait()
	state, err := app.snapshot(runId, input.Path)
	if !assert.NoError(t, err) {
		t.FailNow()
	}
	return state
}

func TestConversationRestoresBytesAcrossTurns(t *testing.T) {
	type input struct{ path replayPath }
	type expected struct{ calls, answers, users int }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	for _, test := range []testCase{
		{"all batches", input{replayBatches}, expected{1, 2, 2}},
		{"capture and tail", input{replayCapture}, expected{1, 2, 2}},
	} {
		t.Run(test.name, func(t *testing.T) {
			app, calls := testApplication(t, 0)
			first := executeAction(t, app, &actionRequest{Action: actionTurn,
				Prompt: "Find John Smith. Remember Cedar.", Path: test.input.path})
			assert.Equal(t, "I found John Smith (C001), using john.smith@email.com.", first.LastAnswer)
			assert.False(t, first.Active)
			assert.Empty(t, app.running)
			assert.True(t, first.HasCapture)
			assert.Greater(t, first.CaptureTail, 0)
			second := executeAction(t, app, &actionRequest{Action: actionTurn, RunId: first.RunId,
				Prompt: "What was my first message and the earlier lookup?", Path: test.input.path})
			assert.Equal(t, first.RunId, second.RunId)
			assert.NotEqual(t, first.TurnId, second.TurnId)
			assert.Equal(t, "Your first message was: Find John Smith. Remember Cedar. "+
				"Earlier lookup: I found John Smith (C001), using john.smith@email.com.",
				second.LastAnswer)
			actual := expected{calls: int(calls.Load())}
			for _, event := range second.Events {
				for _, item := range event.Items {
					if item.Type == gent.AssistantContentFinalAnswer {
						actual.answers++
						assert.NotEmpty(t, item.Sources)
					}
					if item.Label == "You" {
						actual.users++
					}
				}
			}
			assert.Equal(t, test.expected, actual)
			data, _ := app.store.read(first.RunId)
			full, err := app.store.load(first.RunId, data, replayBatches)
			assert.NoError(t, err)
			captured, err := app.store.load(first.RunId, data, replayCapture)
			assert.NoError(t, err)
			fullBytes, err := app.store.codecs.MarshalRunState(full)
			assert.NoError(t, err)
			capturedBytes, err := app.store.codecs.MarshalRunState(captured)
			assert.NoError(t, err)
			assert.Equal(t, fullBytes, capturedBytes)
			idle := executeAction(t, app, &actionRequest{Action: actionContinue,
				RunId: first.RunId, Path: test.input.path})
			assert.Equal(t, &attemptOutcome{Status: gent.ExecutionNoActiveTurn}, idle.Outcome)
			assert.Equal(t, second.LastResult, idle.LastResult)
			assert.Equal(t, second.LastAnswer, idle.LastAnswer)
			assert.Equal(t, second.Head, idle.Head)
			fresh, _ := testApplication(t, 0)
			_, exists := fresh.store.read(first.RunId)
			assert.False(t, exists, "a new server must not claim disk recovery")
		})
	}
}

func waitPreview(t *testing.T, queue chan []byte) *displayEvent {
	t.Helper()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for {
		select {
		case data, ok := <-queue:
			if !assert.True(t, ok) {
				t.FailNow()
			}
			var frame browserFrame
			if !assert.NoError(t, json.Unmarshal(data, &frame)) {
				t.FailNow()
			}
			if frame.Event == nil || frame.Delivery != gent.SubscriberLive {
				continue
			}
			for _, item := range frame.Event.Items {
				if item.Type == gent.AssistantContentReasoning {
					return frame.Event
				}
			}
		case <-timer.C:
			t.Fatal("reasoning preview did not arrive")
		}
	}
}

func TestStopContinueAndReplacement(t *testing.T) {
	type input struct{ action actionKind }
	type expected struct{ sameTurn bool }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	for _, test := range []testCase{
		{"continue", input{actionContinue}, expected{true}},
		{"replace", input{actionTurn}, expected{false}},
	} {
		t.Run(test.name, func(t *testing.T) {
			app, calls := testApplication(t, 3*time.Millisecond)
			runId, status, err := app.start(&actionRequest{Action: actionTurn,
				Prompt: "Find John Smith. Remember Cedar.", Path: replayCapture})
			assert.NoError(t, err)
			assert.Equal(t, 202, status)
			queue, closeQueue := app.hub.subscribe(runId)
			defer closeQueue()
			preview := waitPreview(t, queue)
			old := &actionRequest{RunId: runId, TurnId: preview.TurnId, AttemptId: preview.AttemptId}
			assert.True(t, app.stop(old))
			app.wait.Wait()
			paused, err := app.snapshot(runId, replayCapture)
			assert.NoError(t, err)
			assert.Equal(t, &attemptOutcome{Status: gent.ExecutionPaused}, paused.Outcome)
			assert.Nil(t, paused.LastResult)
			assert.False(t, app.stop(old))
			_, status, err = app.start(&actionRequest{Action: test.input.action, RunId: runId,
				Prompt: "Use the earlier lookup.", Path: replayBatches})
			assert.NoError(t, err)
			assert.Equal(t, 202, status)
			assert.False(t, app.stop(old), "an old attempt cannot stop its replacement")
			app.wait.Wait()
			finished, err := app.snapshot(runId, replayCapture)
			assert.NoError(t, err)
			assert.Equal(t, &attemptOutcome{Status: gent.ExecutionFinished}, finished.Outcome)
			assert.Equal(t, test.expected.sameTurn, paused.TurnId == finished.TurnId)
			assert.Equal(t, int64(1), calls.Load())
		})
	}
}

type failingRecorder struct {
	next  gent.Recorder
	phase gent.ExecutionPhase
}

func (recorder failingRecorder) Save(ctx context.Context, batch *gent.SaveBatch,
	capture func() (*gent.RunState, error),
) error {
	if batch.Checkpoint.Phase == recorder.phase {
		return errors.New("fixture storage rejection")
	}
	return recorder.next.Save(ctx, batch, capture)
}

func TestSaveFailureBlocksDependentWorkAndAnswer(t *testing.T) {
	type input struct{ phase gent.ExecutionPhase }
	type expected struct{ calls int64 }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	for _, test := range []testCase{
		{"admission", input{gent.PhaseInitial}, expected{0}},
		{"before tool", input{gent.PhaseToolReady}, expected{0}},
		{"accepted answer", input{gent.PhaseTerminal}, expected{1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			app, calls := testApplication(t, 0)
			writer := &memoryRecorder{store: app.store}
			session, err := app.newSession(scenarioAirline)
			assert.NoError(t, err)
			assert.NoError(t, session.tools.RegisterCodecs(app.store.codecs))
			runner := app.runner(failingRecorder{writer, test.input.phase}, session)
			execCtx, err := runner.NewExecutionContext(app.ctx, "failure")
			assert.NoError(t, err)
			defer execCtx.Release()
			*writer = *app.store.claim(execCtx.RunId())
			app.sessions[execCtx.RunId()] = session
			value, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Find John Smith")))
			assert.Error(t, err)
			assert.Empty(t, value)
			assert.Nil(t, result)
			assert.Equal(t, test.expected.calls, calls.Load())
			state, err := app.snapshot(execCtx.RunId(), replayBatches)
			assert.NoError(t, err)
			assert.Nil(t, state.LastResult)
			for _, event := range append(state.Events, state.Previews...) {
				for _, item := range event.Items {
					assert.NotEqual(t, gent.AssistantContentFinalAnswer, item.Type)
				}
			}
		})
	}
}

func TestHTTPActionsAndStaleStop(t *testing.T) {
	type input struct{ body string }
	type expected struct {
		status int
		body   string
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	for _, test := range []testCase{
		{"empty task", input{`{"action":"turn"}`}, expected{400, `{"detail":"Enter a task first."}`}},
		{"stale stop", input{`{"action":"stop","runId":"old"}`},
			expected{409, `{"detail":"Stale Stop: the named attempt is not active."}`}},
		{"unknown action", input{`{"action":"steer"}`},
			expected{400, `{"detail":"Unknown action."}`}},
	} {
		t.Run(test.name, func(t *testing.T) {
			app, _ := testApplication(t, 0)
			request := httptest.NewRequest(http.MethodPost, "/api/action",
				strings.NewReader(test.input.body))
			response := httptest.NewRecorder()
			app.routes().ServeHTTP(response, request)
			assert.Equal(t, test.expected, expected{response.Code, response.Body.String()})
		})
	}
}
