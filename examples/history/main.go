// Command history demonstrates application-owned transactions, writer fencing, lost publication,
// and continuation with fresh Go objects. The in-memory store is a teaching adapter, not durable
// storage. A real adapter performs the same checks and writes in a shared database transaction.
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/agents/react"
	"github.com/rickchristie/gent/executor"
	"github.com/rickchristie/gent/toolchain"
	"github.com/rickchristie/gent/trace"
)

type memoryStore struct {
	mu               sync.Mutex
	lease            uint64
	runId            string
	cursor           uint64
	batches          [][]byte
	receipts         map[string][]byte
	runStateSnapshot []byte
	snapshotBatches  int
	updates          chan *gent.SaveBatch
	codecs           *gent.CodecRegistry
}

func (s *memoryStore) loadState() (*gent.RunState, error) {
	s.mu.Lock()
	saved, start := s.runStateSnapshot, s.snapshotBatches
	tail := append([][]byte(nil), s.batches[start:]...)
	runId := s.runId
	s.mu.Unlock()
	state := gent.NewRunState(runId)
	if saved != nil {
		loaded, err := s.codecs.UnmarshalRunState(saved)
		if err != nil {
			return nil, err
		}
		if loaded.Status != gent.RunStateReady {
			return nil, fmt.Errorf("load state: %s: %s", loaded.Status, loaded.Detail)
		}
		state = loaded.State
	}
	for _, data := range tail {
		var batch gent.SaveBatch
		if err := s.codecs.Unmarshal(data, &batch); err != nil {
			return nil, err
		}
		applied, err := state.Apply(&batch, s.codecs)
		if err != nil {
			return nil, err
		}
		if applied.Status != gent.ApplyApplied {
			return nil, fmt.Errorf("apply tail: %s: %s", applied.Status, applied.Detail)
		}
	}
	return state, nil
}

func (s *memoryStore) loadArchive() (*gent.RunView, error) {
	s.mu.Lock()
	batches := append([][]byte(nil), s.batches...)
	runId := s.runId
	s.mu.Unlock()
	archive := gent.NewRunView(runId)
	for _, data := range batches {
		var batch gent.SaveBatch
		if err := s.codecs.Unmarshal(data, &batch); err != nil {
			return nil, err
		}
		applied, err := archive.Apply(&batch, s.codecs)
		if err != nil {
			return nil, err
		}
		if applied.Status != gent.ApplyApplied {
			return nil, fmt.Errorf("apply archive: %s: %s", applied.Status, applied.Detail)
		}
	}
	return archive, nil
}

func (s *memoryStore) nextLease() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lease++
	return s.lease
}

type writer struct {
	store   *memoryStore
	lease   uint64
	stop    gent.ExecutionPhase
	cancel  context.CancelFunc
	stopped bool
}

func (w *writer) Save(ctx context.Context, batch *gent.SaveBatch,
	captureRunStateSnapshot func() (*gent.RunState, error),
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Encode complete batches. A RunStateSnapshot is optional and is published with the same
	// transaction when captured. Old archive bytes remain available for full troubleshooting.
	encoded, err := w.store.codecs.Marshal(batch)
	if err != nil {
		return err
	}
	w.store.mu.Lock()
	if w.lease != w.store.lease {
		w.store.mu.Unlock()
		return errors.New("writer lease expired")
	}
	if prior, exists := w.store.receipts[batch.BatchId]; exists {
		w.store.mu.Unlock()
		if !bytes.Equal(prior, encoded) {
			return errors.New("conflicting duplicate batch")
		}
		return nil
	}
	if batch.PreviousEventSequenceNumber != w.store.cursor ||
		(w.store.runId != "" && w.store.runId != batch.RunId) {
		w.store.mu.Unlock()
		return errors.New("stale writer cursor or wrong run")
	}
	var saved []byte
	if len(w.store.batches)%3 == 0 {
		state, captureErr := captureRunStateSnapshot()
		if captureErr == nil {
			saved, captureErr = w.store.codecs.MarshalRunState(state)
		}
		if captureErr != nil {
			w.store.mu.Unlock()
			return captureErr
		}
	}
	w.store.batches = append(w.store.batches, encoded)
	w.store.receipts[batch.BatchId] = encoded
	w.store.runId = batch.RunId
	w.store.cursor = batch.Events[len(batch.Events)-1].EventSequenceNumber
	if saved != nil {
		w.store.runStateSnapshot, w.store.snapshotBatches = saved, len(w.store.batches)
	}
	w.store.mu.Unlock()
	// Notifications can be lost. A UI reloads the accepted archive after a gap, including a
	// missing terminal notification. The batch and optional RunStateSnapshot are already stored.
	select {
	case w.store.updates <- batch:
	default:
		// The example deliberately requires archive reload when this bounded queue is full.
	}
	if !w.stopped && batch.Checkpoint.Phase == w.stop {
		w.stopped = true
		w.cancel()
	}
	return nil
}

type policyInput struct {
	Id string `json:"id"`
}

type demoModel struct{ answer bool }

func (m *demoModel) GenerateContentStream(
	context.Context,
	*gent.ModelCallRequest,
) (gent.Stream, error) {
	choice := &gent.ContentChoice{
		Completion: gent.ModelCompletion{Status: gent.ModelStatusAnswer},
		Parts: []gent.ContentPart{&gent.AssistantContent{
			Type: gent.AssistantContentFinalAnswerCandidate, MessageId: "answer",
			Content: []gent.ContentPart{gent.TextPart("P10 permits the refund.")}}},
		CandidateSource: &gent.ContentSource{MessageIds: []string{"answer"}},
	}
	if !m.answer {
		choice.CandidateSource = nil
		choice.Completion.Status = gent.ModelStatusToolCalls
		choice.Parts = []gent.ContentPart{&gent.ModelToolCallRequest{ID: "read-policy", Type: "function",
			FunctionCall: &gent.FunctionCall{Name: "read_policy", Arguments: `{"id":"P10"}`}}}
	}
	m.answer = true
	return gent.NewCompletedStream(&gent.ModelCallResponse{
		Choices: []*gent.ContentChoice{choice},
		Info:    &gent.GenerationInfo{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
	}, nil), nil
}

func newTools(calls *int) gent.ToolChain {
	// Recovery returns control to the model. It can request current data if the old read is absent.
	return toolchain.New().RegisterTool(gent.DefineTool("read_policy",
		func(ctx context.Context, input policyInput) (*gent.ToolExecutionResponse,
			error) {
			if _, ok := gent.ToolIdentityFromContext(ctx); !ok {
				return capturedToolResponse("", errors.New("missing tool identity"))
			}
			*calls++
			return capturedToolResponse(input.Id+": refunds are permitted within 30 days", nil)
		}).Description("Read policy evidence").ParameterSchema(nil).
		Recovery(func(context.Context, *gent.ToolRecoveryRequest[policyInput]) (
			*gent.ToolRecoveryResponse, error,
		) {
			return &gent.ToolRecoveryResponse{Status: gent.ToolRecoveryUnresolved,
				Instructions: "The earlier read was not saved. Request current policy data if needed.",
			}, nil
		}).MustBuild())
}

func run(out io.Writer) error {
	for _, stop := range []gent.ExecutionPhase{gent.PhaseToolCompleted, gent.PhaseToolReady} {
		if err := demonstrate(out, stop); err != nil {
			return err
		}
	}
	return nil
}

func demonstrate(out io.Writer, stop gent.ExecutionPhase) error {
	store := &memoryStore{updates: make(chan *gent.SaveBatch, 1),
		codecs: gent.NewCodecRegistry(), receipts: make(map[string][]byte)}
	worker, cancel := context.WithCancel(context.Background())
	defer cancel()
	firstWriter := &writer{store: store, lease: store.nextLease(), stop: stop, cancel: cancel}
	toolCalls := 0
	runner := executor.NewRunner(react.NewAgent(&demoModel{}), &gent.Config{
		Recorder: firstWriter, Codecs: store.codecs, ToolChain: newTools(&toolCalls)})
	execCtx, err := runner.NewExecutionContext(worker, "refund")
	if err != nil {
		return err
	}
	defer execCtx.Release()
	_, _, err = runner.NewTurn(execCtx,
		gent.NewTask(gent.TextPart("May I receive a refund under policy P10?")))
	if !errors.Is(err, context.Canceled) {
		return fmt.Errorf("expected worker interruption, received %v", err)
	}
	state, err := store.loadState()
	if err != nil {
		return err
	}
	next := executor.NewRunner(react.NewAgent(&demoModel{answer: true}), &gent.Config{
		Codecs: store.codecs, ToolChain: newTools(&toolCalls),
		Recorder: &writer{store: store, lease: store.nextLease()}})
	restored, err := next.Restore(context.Background(), state)
	if err != nil {
		return err
	}
	if restored.Status != gent.RestoreReady {
		return fmt.Errorf("restore: %s: %s", restored.Status, restored.Detail)
	}
	defer restored.Context.Release()
	if _, _, err := next.Continue(restored.Context); err != nil {
		return err
	}
	// A late observer finds completion even though the notification queue lost it. Full archive
	// replay and execution-state restoration serve different purposes and use separate types.
	archive, err := store.loadArchive()
	if err != nil {
		return err
	}
	display, err := trace.RestoreSequencer(*archive, trace.Config{Codecs: store.codecs})
	if err != nil {
		return err
	}
	defer display.Close()
	terminal, err := store.loadState()
	if err != nil {
		return err
	}
	finished, err := next.Restore(context.Background(), terminal)
	if err != nil {
		return err
	}
	if finished.Status != gent.RestoreReady {
		return fmt.Errorf("restore completed turn: %s: %s", finished.Status, finished.Detail)
	}
	defer finished.Context.Release()
	answer, result, err := next.GetLastTurnResult(finished.Context)
	if err != nil {
		return err
	}
	if result == nil || result.TerminationReason != gent.TerminationSuccess {
		return errors.New("saved turn has no successful result")
	}
	_, err = fmt.Fprintf(out, "%s: %s; tool executions: %d; saved terminal result reused.\n",
		stop, answer, toolCalls)
	return err
}

func main() {
	if err := run(os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func (m *demoModel) PrepareRequest(execCtx *gent.ExecutionContext,
	request *gent.ModelCallRequest, chain gent.ToolChain,
) (*gent.ModelCallRequest, error) {
	return gent.PrepareModelRequest(execCtx, request, chain)
}
