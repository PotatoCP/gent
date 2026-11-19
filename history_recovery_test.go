package gent_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/agents/react"
	"github.com/rickchristie/gent/toolchain"
	"github.com/rickchristie/gent/trace"
	"github.com/stretchr/testify/assert"
)

type recoveryInput struct {
	Id string `json:"id"`
}

type recoveryModel struct {
	requests []*gent.ModelCallRequest
	answer   bool
}

func (m *recoveryModel) GenerateContentStream(_ context.Context, request *gent.ModelCallRequest,
) (gent.Stream, error) {
	m.requests = append(m.requests, request)
	choice := recoveryAnswerChoice("Policy P10 permits it.")
	if !m.answer && len(m.requests) == 1 {
		choice = recoveryChoice()
	}
	return gent.NewCompletedStream(&gent.ModelCallResponse{Choices: []*gent.ContentChoice{choice},
		Info: &gent.GenerationInfo{InputTokens: 12, OutputTokens: 8, TotalTokens: 20}}, nil), nil
}

func newRecoveryAgent(model *recoveryModel, calls *[]gent.ToolIdentity,
	recovery ...gent.ToolRecoveryFunc[recoveryInput],
) (*react.Agent[string], *gent.Config) {
	var handler gent.ToolRecoveryFunc[recoveryInput]
	if len(recovery) != 0 {
		handler = recovery[0]
	}
	tool := gent.DefineTool("policy", func(ctx context.Context, input recoveryInput) (
		*gent.ToolExecutionResponse, error,
	) {
		identity, ok := gent.ToolIdentityFromContext(ctx)
		if !ok {
			return capturedToolResponse("", errors.New("missing logical tool identity"))
		}
		*calls = append(*calls, identity)
		return capturedToolResponse(input.Id+": exact original text", nil)
	}).Description("Read the original policy").ParameterSchema(nil).Recovery(handler).MustBuild()
	return react.NewAgent(model), &gent.Config{ToolChain: toolchain.New().RegisterTool(tool)}
}

type recoveryStore struct {
	mu               sync.Mutex
	view             gent.RunView
	cancel           context.CancelFunc
	phase            gent.ExecutionPhase
	event            string
	stopped          bool
	after            int
	hits             int
	codecs           *gent.CodecRegistry
	runStateSnapshot []byte
	batches          [][]byte
}

func (s *recoveryStore) Save(_ context.Context, batch *gent.SaveBatch,
	capture func() (*gent.RunState, error),
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	encoded, err := s.codecs.Marshal(batch)
	if err != nil {
		return err
	}
	var owned gent.SaveBatch
	if err := s.codecs.Unmarshal(encoded, &owned); err != nil {
		return err
	}
	if s.view.RunId == "" {
		s.view = *gent.NewRunView(batch.RunId)
	}
	applied, err := s.view.Apply(&owned, s.codecs)
	if err != nil {
		return err
	}
	if applied.Status != gent.ApplyApplied {
		return fmt.Errorf("save archive: %s", applied.Detail)
	}
	state, err := capture()
	if err != nil {
		return err
	}
	s.runStateSnapshot, err = s.codecs.MarshalRunState(state)
	if err != nil {
		return err
	}
	s.batches = append(s.batches, encoded)
	stop := batch.Checkpoint != nil && batch.Checkpoint.Phase == s.phase
	for _, event := range batch.Events {
		stop = stop || event.Type == s.event
	}
	if stop {
		s.hits++
	}
	if stop && s.hits >= s.after && !s.stopped && s.cancel != nil {
		s.stopped = true
		s.cancel()
	}
	return nil
}

func resumeRecovery(t testing.TB, agent *react.Agent[string], saved []byte, config *gent.Config) (
	*gent.ExecutionContext, *gent.ExecutionResult, error,
) {
	t.Helper()
	runner, execCtx := restoreTestExecution(t, agent, config, saved)
	_, result, err := runner.Continue(execCtx)
	return execCtx, result, err
}

func saveInterruptedRun(t *testing.T, phase gent.ExecutionPhase, event string,
) ([]byte, []*gent.ModelCallRequest, []gent.ToolIdentity) {
	t.Helper()
	worker, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := &recoveryStore{
		codecs: gent.NewCodecRegistry(),
		cancel: cancel,
		phase:  phase,
		event:  event,
	}
	model := &recoveryModel{}
	var calls []gent.ToolIdentity
	agent, config := newRecoveryAgent(model, &calls)
	config.Recorder, config.Codecs = store, store.codecs
	runner, execCtx := newTestExecution(t, worker, agent, config)
	_, result, err := runner.NewTurn(execCtx,
		gent.NewTask(gent.TextPart("Read policy P10 twice, then answer.")))
	if !assert.ErrorIs(t, err, context.Canceled) {
		t.FailNow()
	}
	assert.Nil(t, result)
	assert.NotEqual(t, gent.PhaseTerminal, store.view.Checkpoint.Phase)
	return store.runStateSnapshot, model.requests, calls
}

func TestHistoryRecovery_Boundaries(t *testing.T) {
	type input struct {
		phase gent.ExecutionPhase
		event string
	}
	type expected struct{ modelsBefore, modelsAfter, toolsBefore, toolsAfter int }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"initial", input{phase: gent.PhaseInitial}, expected{0, 2, 0, 2}},
		{"iteration ready", input{phase: gent.PhaseIterationReady}, expected{0, 2, 0, 2}},
		{"saved response at before tool", input{event: "model_call_finished"}, expected{1, 1, 0, 0}},
		{"before tool", input{phase: gent.PhaseToolReady}, expected{1, 1, 0, 0}},
		{"first tool completed", input{phase: gent.PhaseToolCompleted}, expected{1, 1, 1, 0}},
		{"iteration completed", input{phase: gent.PhaseIterationCompleted}, expected{1, 1, 2, 0}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			saved, requests, earlierCalls := saveInterruptedRun(t, test.input.phase, test.input.event)
			// Only encoded state crosses the process boundary. Runtime pointers are not reused.
			model := &recoveryModel{answer: len(requests) > 0}
			var calls []gent.ToolIdentity
			agent, config := newRecoveryAgent(model, &calls)
			runner, execCtx := restoreTestExecution(t, agent, config, saved)
			_, result, err := runner.Continue(execCtx)
			if !assert.NoError(t, err) {
				return
			}
			assert.Equal(t, []gent.ContentPart{gent.TextPart("Policy P10 permits it.")}, result.Output)
			assert.Equal(t, test.expected, expected{
				len(requests), len(model.requests), len(earlierCalls), len(calls)})
			assert.Equal(t, int64(40), execCtx.Stats().GetCounter(gent.SCTotalTokens))
			// A saved BeforeToolCall also counts the interrupted call, even if it never ran.
			expectedTools := test.expected.toolsBefore + test.expected.toolsAfter
			if test.input.phase == gent.PhaseToolReady || test.input.event != "" {
				expectedTools++
			}
			assert.Equal(t, int64(expectedTools), execCtx.Stats().GetCounter(gent.SCToolCalls))
			allCalls := append(earlierCalls, calls...)
			if len(allCalls) == 2 {
				assert.NotEqual(t, allCalls[0].ToolCallId, allCalls[1].ToolCallId)
			}
			value, out, err := runner.GetLastTurnResult(execCtx)
			assert.NoError(t, err)
			assert.Equal(t, "Policy P10 permits it.", value)
			assert.Equal(t, result, out)
			value, out, err = runner.Continue(execCtx)
			assert.NoError(t, err)
			assert.Empty(t, value)
			assert.Equal(t, &gent.ExecutionResult{Status: gent.ExecutionNoActiveTurn}, out)
		})
	}
}

type recoveryFile struct{ RunStateSnapshot []byte }

func TestHistoryRecovery_FreshProcess(t *testing.T) {
	mode := os.Getenv("GENT_HISTORY_PROCESS")
	path := os.Getenv("GENT_HISTORY_BUNDLE")
	if mode == "produce" {
		saved, _, calls := saveInterruptedRun(t, gent.PhaseToolCompleted, "")
		assert.Len(t, calls, 1)
		data, err := json.Marshal(recoveryFile{RunStateSnapshot: saved})
		assert.NoError(t, err)
		assert.NoError(t, os.WriteFile(path, data, 0600))
		return
	}
	if mode == "continue" {
		data, err := os.ReadFile(path)
		assert.NoError(t, err)
		var file recoveryFile
		assert.NoError(t, json.Unmarshal(data, &file))
		model := &recoveryModel{answer: true}
		var calls []gent.ToolIdentity
		agent, config := newRecoveryAgent(model, &calls)
		config = testConfig(t, agent, config)
		seq, err := trace.NewSequencer("", trace.Config{Codecs: config.Codecs})
		assert.NoError(t, err)
		defer seq.Close()
		config.Subscribers = []*gent.SubscriberRegistration{{Subscriber: seq}}
		execCtx, result, err := resumeRecovery(t, agent, file.RunStateSnapshot, config)
		if !assert.NoError(t, err) {
			return
		}
		assert.Equal(t, []gent.ContentPart{gent.TextPart("Policy P10 permits it.")}, result.Output)
		assert.Empty(t, calls)
		assert.Len(t, model.requests, 1)
		assert.Equal(t, int64(40), execCtx.Stats().GetCounter(gent.SCTotalTokens))
		baselineModel := &recoveryModel{}
		var baselineCalls []gent.ToolIdentity
		baselineAgent, baselineConfig := newRecoveryAgent(baselineModel, &baselineCalls)
		runner, baseline := newTestExecution(t, context.Background(), baselineAgent, baselineConfig)
		_, _, err = runner.NewTurn(baseline,
			gent.NewTask(gent.TextPart("Read policy P10 twice, then answer.")))
		assert.NoError(t, err)
		want, actual := baselineModel.requests[1].Messages, model.requests[0].Messages
		assert.Equal(t, want[0], actual[0])
		// Physical model IDs differ between runs; tool request and reply content stays exact.
		assert.Equal(t, gent.ContentText(want[1].Parts), gent.ContentText(actual[1].Parts))
		assert.Equal(t, want[1].Parts[1:], actual[1].Parts[1:])
		assert.Equal(t, want[2], actual[2])
		reply := actual[3].Parts[0].(*gent.ModelToolCallResponse)
		status, present := reply.RecoveryStatus()
		assert.True(t, present)
		assert.Equal(t, gent.ToolRecoveryNotStarted, status)
		assert.Equal(t, baselineModel.requests[1].Options, model.requests[0].Options)
		return
	}
	path = filepath.Join(t.TempDir(), "run.json")
	for _, mode := range []string{"produce", "continue"} {
		command := exec.Command(os.Args[0], "-test.run=^TestHistoryRecovery_FreshProcess$")
		command.Env = append(os.Environ(), "GENT_HISTORY_PROCESS="+mode, "GENT_HISTORY_BUNDLE="+path)
		result, err := command.CombinedOutput()
		if !assert.NoError(t, err, "%s", result) {
			return
		}
	}
}

func recoveryAnswerChoice(text string) *gent.ContentChoice {
	return &gent.ContentChoice{Completion: gent.ModelCompletion{Status: gent.ModelStatusAnswer},
		CandidateSource: &gent.ContentSource{MessageIds: []string{"answer"}},
		Parts: []gent.ContentPart{&gent.AssistantContent{MessageId: "answer",
			Type:    gent.AssistantContentFinalAnswerCandidate,
			Content: []gent.ContentPart{gent.TextPart(text)}}}}
}

func recoveryChoice() *gent.ContentChoice {
	choice := &gent.ContentChoice{Completion: gent.ModelCompletion{Status: gent.ModelStatusToolCalls},
		Parts: []gent.ContentPart{&gent.AssistantContent{MessageId: "plan",
			Type:    gent.AssistantContentCommentary,
			Content: []gent.ContentPart{gent.TextPart("Read the policy.")}}}}
	for _, id := range []string{"policy-a", "policy-b"} {
		choice.Parts = append(choice.Parts, &gent.ModelToolCallRequest{ID: id, Type: "function",
			FunctionCall: &gent.FunctionCall{Name: "policy", Arguments: `{"id":"P10"}`}})
	}
	return choice
}

func (m *recoveryModel) PrepareRequest(execCtx *gent.ExecutionContext,
	request *gent.ModelCallRequest, chain gent.ToolChain,
) (*gent.ModelCallRequest, error) {
	return gent.PrepareModelRequest(execCtx, request, chain)
}
