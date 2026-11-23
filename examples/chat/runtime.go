package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/compaction"
	"github.com/rickchristie/gent/executor"
	"github.com/rickchristie/gent/format"
	"github.com/rickchristie/gent/search"
)

type actionKind string

const (
	// actionTurn admits and executes the first task or a replacement task on an idle run.
	actionTurn actionKind = "turn"
	// actionContinue resumes the unfinished accepted task without adding another prompt.
	actionContinue actionKind = "continue"
	// actionRestore validates saved bytes and reads the last completed result without execution.
	actionRestore actionKind = "restore"
	// actionStop requests a pause only for the exact active turn and attempt named by the browser.
	actionStop actionKind = "stop"
)

type actionRequest struct {
	Scenario  scenarioId `json:"scenario"`
	Action    actionKind `json:"action"`
	RunId     string     `json:"runId"`
	Prompt    string     `json:"prompt"`
	Path      replayPath `json:"path"`
	TurnId    string     `json:"turnId"`
	AttemptId string     `json:"attemptId"`
}

type attemptOutcome struct {
	Status gent.ExecutionStatus `json:"status"`
	Detail string               `json:"detail,omitempty"`
}

type browserState struct {
	Scenario    scenarioId            `json:"scenario"`
	RunId       string                `json:"runId"`
	Provider    string                `json:"provider"`
	Head        uint64                `json:"head"`
	BatchCount  int                   `json:"batchCount"`
	CaptureTail int                   `json:"captureTail"`
	HasCapture  bool                  `json:"hasCapture"`
	Active      bool                  `json:"active"`
	TurnId      string                `json:"turnId"`
	AttemptId   string                `json:"attemptId"`
	Events      []*displayEvent       `json:"events"`
	Previews    []*displayEvent       `json:"previews"`
	PreviewGap  bool                  `json:"previewGap"`
	LastAnswer  string                `json:"lastAnswer"`
	LastResult  *gent.ExecutionResult `json:"lastResult"`
	Outcome     *attemptOutcome       `json:"outcome"`
}

type application struct {
	// HTTP reads hold this gate only while preparing a response, never for the SSE lifetime.
	// Reset excludes admission and reads, then joins workers before removing their saved bytes.
	lifecycle  sync.RWMutex
	serverId   string
	ctx        context.Context
	store      *memoryStore
	hub        *eventHub
	newSession func(scenarioId) (*scenarioSession, error)
	sessions   map[string]*scenarioSession
	model      func() gent.Model
	provider   string
	mu         sync.Mutex
	running    map[string]*gent.ExecutionContext
	outcomes   map[string]*attemptOutcome
	cancels    map[string]context.CancelFunc
	chats      map[string]*conversationInfo
	wait       sync.WaitGroup
}

func newApplication(ctx context.Context, model func() gent.Model, provider string,
	embedder search.Embedder,
) *application {
	return &application{ctx: ctx, serverId: rand.Text(),
		store: newMemoryStore(gent.NewCodecRegistry()), hub: newEventHub(),
		model: model, provider: provider, sessions: map[string]*scenarioSession{},
		newSession: func(id scenarioId) (*scenarioSession, error) { return newScenario(id, embedder) },
		running:    map[string]*gent.ExecutionContext{}, outcomes: map[string]*attemptOutcome{},
		cancels: map[string]context.CancelFunc{}, chats: map[string]*conversationInfo{}}
}

func (app *application) runner(recorder gent.Recorder,
	session *scenarioSession,
) *executor.Runner[string] {
	model := app.model()
	return executor.NewRunner(newAgent(model, app.provider), &gent.Config{
		Codecs: app.store.codecs, Recorder: recorder, ToolChain: session.tools,
		Limits: []gent.Limit{{Type: gent.LimitExactKey, Key: gent.SCIterations,
			Scope: gent.LimitTurn, MaxValue: session.config.MaxIterations}},
		Hooks: session.hooks(),
		Subscribers: []*gent.SubscriberRegistration{
			{Subscriber: &eventSubscriber{app.hub, gent.SubscriberLive, app.store.codecs},
				Delivery: gent.SubscriberLive},
			{Subscriber: &eventSubscriber{app.hub, gent.SubscriberAccepted, app.store.codecs}},
		},
		CompactionTrigger:  compaction.NewStatThresholdTrigger().OnGauge(gent.SGScratchpadLength, 30),
		CompactionStrategy: compaction.NewSummarization(model, format.NewXML()),
	})
}

func (app *application) restore(ctx context.Context, runner *executor.Runner[string], runId string,
	data *storedBytes, path replayPath,
) (*gent.ExecutionContext, error) {
	state, err := app.store.load(runId, data, path)
	if err != nil {
		return nil, err
	}
	restored, err := runner.Restore(ctx, state)
	if err != nil {
		return nil, err
	}
	if restored.Status != gent.RestoreReady {
		return nil, fmt.Errorf("restore: %s: %s", restored.Status, restored.Detail)
	}
	return restored.Context, nil
}

// The application serializes routing with worker ownership. A stale Stop cannot race through
// admission into another attempt, and NewTurn cannot overlap a running Continue.
func (app *application) start(input *actionRequest) (string, int, error) {
	app.mu.Lock()
	defer app.mu.Unlock()
	if app.running[input.RunId] != nil {
		return input.RunId, 409, errors.New("This conversation is running. Stop it first.")
	}
	if len(app.running) >= 16 {
		return "", 429, errors.New("All 16 worker slots are in use.")
	}
	ctx, cancel := context.WithTimeout(app.ctx, 3*time.Minute)
	var execCtx *gent.ExecutionContext
	started := false
	defer func() {
		if !started {
			cancel()
			if execCtx != nil {
				execCtx.Release()
			}
		}
	}()
	var runner *executor.Runner[string]
	var session *scenarioSession
	var err error
	if input.RunId == "" {
		if input.Action != actionTurn {
			return "", 400, errors.New("Start a conversation first.")
		}
		if input.Scenario == "" {
			input.Scenario = scenarioAirline
		}
		if input.Scenario != scenarioAirline && input.Scenario != scenarioEcommerce {
			return "", 400, errors.New("Choose an airline or e-commerce scenario.")
		}
		session, err = app.newSession(input.Scenario)
		if err != nil {
			return "", 500, err
		}
		if err := session.tools.RegisterCodecs(app.store.codecs); err != nil {
			return "", 500, err
		}
		writer := &memoryRecorder{store: app.store}
		runner = app.runner(writer, session)
		execCtx, err = runner.NewExecutionContext(ctx, "conversation-lab")
		if err != nil {
			return "", 500, err
		}
		*writer = *app.store.claim(execCtx.RunId())
	} else {
		if _, exists := app.store.read(input.RunId); !exists {
			return "", 404, errors.New("Conversation not found. A server restart clears memory.")
		}
		session = app.sessions[input.RunId]
		if input.Scenario != "" && input.Scenario != session.id {
			return "", 409, errors.New("Start a new chat to change the scenario.")
		}
		writer := app.store.claim(input.RunId)
		data, _ := app.store.read(input.RunId)
		runner = app.runner(writer, session)
		execCtx, err = app.restore(ctx, runner, input.RunId, data, input.Path)
		if err != nil {
			return "", 500, err
		}
	}
	runId := execCtx.RunId()
	app.sessions[runId] = session
	app.running[runId] = execCtx
	app.cancels[runId] = cancel
	if app.chats[runId] == nil {
		app.chats[runId] = &conversationInfo{RunId: runId, Scenario: session.id,
			Title: conversationTitle(input.Prompt)}
	}
	app.chats[runId].UpdatedAt = time.Now()
	delete(app.outcomes, runId)
	app.wait.Add(1)
	started = true
	go func() {
		defer app.wait.Done()
		defer cancel()
		var result *gent.ExecutionResult
		var err error
		if input.Action == actionContinue {
			_, result, err = runner.Continue(execCtx)
		} else {
			task := gent.NewTask(gent.TextPart(input.Prompt))
			if input.RunId == "" {
				task.Instructions = []*gent.ModelMessage{
					gent.TextParts(gent.ChatMessageTypeSystem, session.instructions()),
				}
			}
			_, result, err = runner.NewTurn(execCtx, task)
		}
		execCtx.Release()
		outcome := &attemptOutcome{}
		if err != nil {
			log.Printf("run %s: %v", runId, err)
			outcome.Detail = "Execution stopped. Inspect the server log, fix the cause, then restore."
		} else {
			outcome.Status = result.Status
		}
		app.mu.Lock()
		delete(app.running, runId)
		delete(app.cancels, runId)
		app.outcomes[runId] = outcome
		app.chats[runId].UpdatedAt = time.Now()
		// Publish idle before another action can own this run. Its browser reload sees accepted
		// storage, not an old pointer. The hub never calls back into the application lock.
		if err := app.hub.publish(runId, &browserFrame{Kind: frameIdle}); err != nil {
			log.Printf("display idle: %v", err)
		}
		app.mu.Unlock()
	}()
	return runId, 202, nil
}

func (app *application) stop(input *actionRequest) bool {
	app.mu.Lock()
	defer app.mu.Unlock()
	execCtx := app.running[input.RunId]
	if execCtx == nil || input.TurnId == "" || input.AttemptId == "" ||
		execCtx.TurnId() != input.TurnId || execCtx.AttemptId() != input.AttemptId {
		return false
	}
	execCtx.Stop()
	return true
}

func (app *application) snapshot(runId string, path replayPath) (*browserState, error) {
	data, exists := app.store.read(runId)
	if !exists {
		return nil, errors.New("Conversation not found. A server restart clears memory.")
	}
	app.mu.Lock()
	session := app.sessions[runId]
	app.mu.Unlock()
	state := &browserState{Scenario: session.id, RunId: runId, Provider: app.provider, Head: data.head,
		BatchCount: len(data.batches), HasCapture: data.capture != nil,
		CaptureTail: len(data.batches) - data.tail}
	if data.head > 0 {
		view, err := app.store.archive(runId, data)
		if err != nil {
			return nil, err
		}
		for _, event := range view.Activity {
			projected, err := project(event, app.store.codecs)
			if err != nil {
				return nil, err
			}
			state.Events = append(state.Events, projected)
		}
		runner := app.runner(nil, session)
		execCtx, err := app.restore(app.ctx, runner, runId, data, path)
		if err != nil {
			return nil, err
		}
		defer execCtx.Release()
		state.LastAnswer, state.LastResult, err = runner.GetLastTurnResult(execCtx)
		if err != nil {
			return nil, err
		}
		state.TurnId = execCtx.TurnId()
	}
	app.mu.Lock()
	if execCtx := app.running[runId]; execCtx != nil {
		state.Active, state.TurnId, state.AttemptId = true, execCtx.TurnId(), execCtx.AttemptId()
	}
	state.Outcome = app.outcomes[runId]
	app.mu.Unlock()
	state.Previews, state.PreviewGap = app.hub.previews(runId, data.head)
	return state, nil
}
