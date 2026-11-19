package gent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"sync/atomic"
	"time"
)

// RunSchemaVersion identifies the saved format used by SaveBatch, RunView, and Checkpoint.
// Batch application and recovery require this version; it is separate from custom codec versions.
const RunSchemaVersion = 7

// RecordedEvent carries ordered execution evidence with a concrete, frozen payload.
// Consumers MUST NOT modify this envelope or any value reachable through its fields.
// Read fields only, including nested payload pointers, slices, maps, and byte buffers.
// Deduplicate Live previews by RunId, AttemptId, and EventSequenceNumber.
// Deduplicate Accepted events by RunId and EventSequenceNumber. A failed OnEvent can have sent
// data already; a delivery cursor is not a browser acknowledgment. Unsaved positions can be reused
// by another attempt. EventSequenceNumber defines order; Ts is for display and diagnostics.
type RecordedEvent struct {
	EventSequenceNumber uint64    `json:"eventSequenceNumber"`
	RunId               string    `json:"runId"`
	TurnId              string    `json:"turnId"`
	Ts                  time.Time `json:"ts"`
	Type                string    `json:"type"`
	EventName           string    `json:"eventName,omitempty"`
	ContextId           string    `json:"contextId"`
	Iteration           int       `json:"iteration"`
	AttemptId           string    `json:"attemptId"`
	ModelCallId         string    `json:"modelCallId,omitempty"`
	ToolCallId          string    `json:"toolCallId,omitempty"`
	// Payload is borrowed read-only, including all nested values. Known kinds use concrete
	// pointers such as *StreamChunk or *HistoryChange. Structural markers can have nil payloads.
	// CodecRegistry preserves these concrete values; consumers need no decoding step.
	Payload any `json:"payload,omitempty"`
}

// RuntimeState preserves accepted accounting and identity allocation for the whole run.
// Approved saves accept it with the matching control checkpoint. Recovery restores these totals
// without recounting usage or allocating an identity that accepted work already used.
type RuntimeState struct {
	// Contexts contains the single root context accounting value. Saved-state validation rejects
	// multiple contexts; child execution is not supported.
	Contexts []ContextRuntime `json:"contexts"`

	// Allocation is the accepted history identity high-water mark.
	// A new identity increments it before use. It must not decrease when a batch is applied.
	Allocation uint64 `json:"allocation"`
}

// ContextRuntime is one context's accounting at a save boundary. Counters and gauges are absolute
// values, not changes to replay. The context's control state is stored in ContextCheckpoint.
type ContextRuntime struct {
	// TurnCounterBaselines keep cumulative accounting separate from the current turn allowance.
	TurnCounterBaselines map[string]int64 `json:"turnCounterBaselines,omitempty"`

	// ContextId identifies the context whose accounting this snapshot contains.
	ContextId string `json:"contextId"`

	// Iteration is the cumulative context iteration at this accepted savepoint.
	Iteration int `json:"iteration"`

	// Counters contains all accumulated counter totals, keyed by the string form of StatKey.
	// Totals cannot decrease, and a later snapshot must retain previously saved counter keys.
	Counters map[string]int64 `json:"counters"`

	// Gauges contains the current local gauge values, keyed by the string form of StatKey.
	// Values can increase, decrease, or reset; they are restored without publishing stats events.
	Gauges map[string]float64 `json:"gauges"`

	// NextModelCall stores the last allocated automatic model-call number. A new automatic ID
	// increments this value before use. Zero means no automatic model-call ID has been allocated.
	NextModelCall uint64 `json:"nextModelCall"`

	// NextToolCall stores the last allocated tool-call number. A new ID increments this value
	// before use. Preserving it prevents new work from reusing a saved logical call identity.
	NextToolCall uint64 `json:"nextToolCall"`

	// ExceededLimit is the limit that stopped this context, or nil when no limit was exceeded.
	// Recovery preserves that stop condition instead of granting the run a new usage allowance.
	ExceededLimit *Limit `json:"exceededLimit,omitempty"`
}

// SaveBatch is one persistence request for a consecutive group of events, accepted runtime, and
// at most one new control checkpoint. It can cover part of an iteration or several local steps.
//
// Sequence numbers belong to RecordedEvent.EventSequenceNumber. For example, a request with
// PreviousEventSequenceNumber = 1 and event numbers [2, 3, 4, 5] advances the saved position to 5.
// Its first event is PreviousEventSequenceNumber + 1; its last event supplies the new saved
// position.
// There is no separate save-request sequence number. BatchId identifies the request for retries.
//
// SaveBatch must be stored atomically. Adapters enforce PreviousEventSequenceNumber and accept
// the same
// BatchId/digest only once. The application also checks its worker lease in that transaction.
// Use CodecRegistry.Marshal for portable JSON that preserves concrete content and metadata types.
type SaveBatch struct {
	// SchemaVersion identifies this request's saved format and must equal RunSchemaVersion.
	SchemaVersion int `json:"schemaVersion"`

	// RunId identifies the run to update. The view, all events, and any supplied checkpoint
	// must have this same identity.
	RunId string `json:"runId"`

	// BatchId is the BatchDigest of this exact request, including its typed values. It is a
	// content identity, not an ordinal. Keep it and all request data unchanged for an exact retry.
	BatchId string `json:"batchId"`

	// PreviousEventSequenceNumber is the saved cursor expected before this request. It must match
	// RunView.LastAppliedEventSequenceNumber for a new batch; zero means this is the first batch.
	// An already accepted exact duplicate is recognized before this cursor check.
	PreviousEventSequenceNumber uint64 `json:"previousEventSequenceNumber"`

	// Events contains a nonempty, ordered sequence with no gaps or nil entries. The first number
	// is PreviousEventSequenceNumber + 1. Store the full sequence in the same transaction as Runtime
	// and Checkpoint; applying only part of it would invalidate recovery state.
	Events []*RecordedEvent `json:"events"`

	// Checkpoint supplies the resumable control state. Gent includes it at every approved
	// savepoint. Its EventSequenceNumber equals the last event in Events; Revision matches the
	// resulting history. Applications must store it with the unchanged batch. Replay can retain
	// a previous checkpoint when this field is nil, but that is not a new execution savepoint.
	Checkpoint *Checkpoint `json:"checkpoint,omitempty"`

	// Runtime contains accounting and allocation state at the end of this event group. Gent
	// captures it with the matching control checkpoint at each approved savepoint.
	Runtime RuntimeState `json:"runtime"`
}

// Recorder is the application's persistence boundary. Configure it in Config when execution
// must survive worker replacement. Without a recorder, accepted state remains only in memory.
// Storage reads and worker ownership belong to the application; Gent owns capture and replay.
//
// # Recovery invariant
//
// Gent guarantees the following invariant at each accepted savepoint:
//
//	full batch replay == RunStateSnapshot + accepted tail == accepted logical state
//
// Both replay paths must reach the same accepted event sequence position. Gent must retain all
// custom LoopData, required history, counters, accepted results, operation IDs, and framework
// continuation and recovery evidence. A RunStateSnapshot is optional replay acceleration; it must
// never be the only saved copy of custom data or the last completed turn's result. The application
// stores complete batches through Save; Gent owns coherent state capture, encoding, and replay.
//
// The invariant restores saved logical state, not a Go stack or identical future model output.
// Custom Next restarts at its entry point on restored data. Its author owns safe repeated local
// work and new operations. Managed recovery must reuse accepted results for the same operation and
// completed recovery preparation; it does not undo external effects or cover unmanaged calls.
type Recorder interface {
	// Save blocks until the full batch has committed. Return nil only after durable acceptance;
	// adding the request to an asynchronous queue does not satisfy this contract.
	//
	// Store events, history changes, runtime, the optional checkpoint, cursor, and duplicate
	// receipt atomically. Check the expected cursor and the application's worker lease or fence
	// in that same transaction. RunState.Apply and RunView.Apply validate replay, not storage leases.
	// An exact retry must succeed without applying the same effects twice.
	//
	// Gent admits a fixed batch only after checking for an unexpected attempt failure. A later
	// failure cannot withdraw that admitted request or change its contents. Complete the save
	// under the normal acceptance and uncertain-storage rules; report any storage failure.
	//
	// Honor the supplied context deadline and return errors to stop further dependent execution.
	// A lost acknowledgment can return an error after commit; it does not prove that nothing
	// was stored. Keep socket delivery separate: its failure cannot undo a committed save.
	// The batch and all reachable values are fixed and read-only. Persist them without mutation.
	// captureRunStateSnapshot captures the candidate state through this exact batch, including
	// its custom data and framework continuation. Call it only while Save is active; do not
	// retain the callback or reenter acceptance. Capture is lazy and does not recapture live data.
	// Each returned RunState is independent and stable and can be encoded after Save returns.
	// Publish a RunStateSnapshot only with or after its durable batch, never ahead of the batch.
	Save(
		ctx context.Context, batch *SaveBatch,
		captureRunStateSnapshot func() (*RunState, error),
	) error
}

// RunView reconstructs the complete accepted archive from stored SaveBatch values. Apply advances
// its cursor and contents together without executing models, tools, Hooks, or output processing.
// It preserves original prompts, native and readable evidence, and compaction changes. It is an
// inspection view; use RunState for execution restoration. A compact RunStateSnapshot cannot
// replace earlier batches when the application needs the full archive.
type RunView struct {
	// replay is the compact shared reducer, not a second archive.
	replay *RunState

	// SchemaVersion identifies the saved format and must equal RunSchemaVersion.
	SchemaVersion int `json:"schemaVersion"`

	// RunId identifies the logical run across all of its worker attempts.
	RunId string `json:"runId"`

	// LastAppliedEventSequenceNumber is the final accepted event number, or zero for an empty view.
	// It equals the length of Activity and can be ahead of Checkpoint.EventSequenceNumber. A live
	// delivery cursor is separate and does not establish which events storage has accepted.
	LastAppliedEventSequenceNumber uint64 `json:"lastAppliedEventSequenceNumber"`

	// History contains accepted history entries and selection changes reconstructed from
	// Activity. Checkpoint contexts select entries from this history by ID and revision.
	History HistorySnapshot `json:"history"`

	// Activity contains every accepted event in order, from 1 to LastAppliedEventSequenceNumber.
	// The archive retains this complete sequence. Compact RunState retains only evidence needed
	// for execution; it cannot replace this archive for inspection of earlier work.
	Activity []*RecordedEvent `json:"activity"`

	// Checkpoint is the latest accepted control state, or nil before any checkpoint is saved.
	// Gent includes a checkpoint at every approved execution savepoint.
	Checkpoint *Checkpoint `json:"checkpoint,omitempty"`

	// Runtime is the latest accepted accounting and allocation state. Recovery uses it without
	// recounting usage or assigning an accepted operation's identity to new work.
	Runtime RuntimeState `json:"runtime"`

	// Batches maps accepted BatchId values to their digests. Apply uses it to recognize
	// exact retries even after the saved cursor advances. It does not contain batch snapshots.
	Batches map[string]string `json:"batches"`
}

var (
	// ErrEventGap means the expected saved cursor or consecutive event sequence does not match.
	// An empty SaveBatch.Events also fails this requirement.
	ErrEventGap = errors.New("gent: event sequence gap")

	// ErrWrongRun means a batch or event does not belong to the view's logical run.
	ErrWrongRun = errors.New("gent: wrong run identity")

	// ErrHistoryConflict means a history transition conflicts with accepted history state.
	ErrHistoryConflict = errors.New("gent: history revision conflict")

	// ErrPersistenceStopped means new acceptance was attempted while an earlier save was
	// unresolved. Reconcile that exact request before resuming execution on a fresh context.
	ErrPersistenceStopped = errors.New("gent: recording stopped after a save failure")
)

// PersistenceError retains the exact uncertain batch. Do not issue new external work until the
// application reconciles this batch with storage or restores the last accepted run.
type PersistenceError struct {
	// Batch is the exact failed or uncertain save request. Preserve its identity, event numbers,
	// and contents for reconciliation; a storage error does not prove that it was not committed.
	Batch *SaveBatch

	// Cause is the underlying failure. A blocked request also wraps ErrPersistenceStopped.
	// Unwrap exposes this error to errors.Is and errors.As.
	Cause error
}

// Error identifies the affected save request and includes its cause.
func (e *PersistenceError) Error() string {
	return fmt.Sprintf("gent: save batch %s: %v", e.Batch.BatchId, e.Cause)
}

// Unwrap returns the underlying save failure or stopped-persistence error.
func (e *PersistenceError) Unwrap() error { return e.Cause }

// accept runs under flushMu. Event numbers were fixed before live delivery; only this path
// advances accepted state after the recorder confirms the complete group.
func (run *executionRun) accept(ctx context.Context, input *SaveBatch) (*SaveBatch, error) {
	if run.failed != nil {
		batch, err := cloneSaveBatch(run.codecs, run.failed.Batch)
		if err != nil {
			return nil, err
		}
		return nil, &PersistenceError{Batch: batch,
			Cause: errors.Join(ErrPersistenceStopped, run.failed.Cause)}
	}
	run.mu.RLock()
	view := run.view
	run.mu.RUnlock()
	// Pending payloads and sequence numbers are frozen before live delivery. Copy their envelopes
	// without changing the identities already seen by subscribers.
	events := input.Events
	batch := &SaveBatch{
		SchemaVersion:               RunSchemaVersion,
		RunId:                       view.RunId,
		PreviousEventSequenceNumber: view.LastAppliedEventSequenceNumber,
		Events: make(
			[]*RecordedEvent,
			len(
				events,
			),
		),
		Runtime: input.Runtime,
	}
	for i, pending := range events {
		if pending == nil {
			return nil, errors.New("gent: nil recorded event")
		}
		event := *pending
		if event.RunId != "" && event.RunId != view.RunId {
			return nil, ErrWrongRun
		}
		if event.EventSequenceNumber != view.LastAppliedEventSequenceNumber+uint64(i)+1 {
			return nil, ErrEventGap
		}
		batch.Events[i] = &event
	}
	if input.Checkpoint != nil {
		checkpoint := *input.Checkpoint
		checkpoint.EventSequenceNumber = batch.Events[len(batch.Events)-1].EventSequenceNumber
		batch.Checkpoint = &checkpoint
	}
	var err error
	batch.BatchId, err = BatchDigest(batch, run.codecs)
	if err != nil {
		return nil, err
	}
	return run.save(ctx, view, batch)
}

func (run *executionRun) save(
	ctx context.Context, view RunView, batch *SaveBatch,
) (*SaveBatch, error) {
	state := view.replay
	if state == nil {
		var result *ApplyStateResult
		state, result = view.replayState()
		if result != nil {
			return nil, fmt.Errorf("gent: prepare accepted state: %s", result.Detail)
		}
	}
	candidate, result := state.prepare(batch)
	if result != nil {
		return nil, fmt.Errorf("gent: prepare batch %q: %s", batch.BatchId, result.Detail)
	}
	// This check is batch admission, ordered with stopRecording by run.mu. Capture, encoding,
	// and candidate preparation must finish first: each can overlap a failed parallel operation.
	// Once admitted, the fixed batch keeps its storage outcome even if failure follows. Never
	// hold run.mu across Recorder.Save: a callback can read the run or wait for worker cleanup.
	run.mu.RLock()
	err := run.err
	run.mu.RUnlock()
	if err != nil {
		return nil, err
	}
	if run.recorder != nil {
		prepared := &RunState{data: candidate}
		var capturing atomic.Bool
		capturing.Store(true)
		capture := func() (*RunState, error) {
			if !capturing.Load() {
				return nil, errors.New("gent: capture RunStateSnapshot only during Recorder.Save")
			}
			return prepared.capture(run.codecs)
		}
		err := run.recorder.Save(ctx, batch, capture)
		capturing.Store(false)
		if err != nil {
			run.failed = &PersistenceError{Batch: batch, Cause: err}
			return nil, &PersistenceError{Batch: batch, Cause: err}
		}
	}
	run.mu.Lock()
	acceptedState := &RunState{data: candidate}
	run.view = acceptedState.projection()
	run.view.Activity = candidate.Evidence
	run.view.replay = acceptedState
	run.working = retainedHistory(run.working, candidate.Checkpoint)
	run.root.lastOutcome = candidate.LastOutcome
	run.mu.Unlock()
	run.failed = nil
	return batch, nil
}

// BatchDigest includes typed values, so distinct numeric or custom content types cannot share
// an identity merely because ordinary JSON would render them alike.
// It returns a SHA-256 hexadecimal digest of the request with BatchId cleared. Supply the run's
// codec registry for custom types; omitting it uses the default registry. Gent uses this digest
// as BatchId, and Apply recomputes it before accepting a new request or an exact duplicate.
func BatchDigest(batch *SaveBatch, registries ...*CodecRegistry) (string, error) {
	if batch == nil {
		return "", errors.New("gent: nil save batch")
	}
	return batchDigest(batch, newValueEncoder(registryOrDefault(registries)))
}

func batchDigest(batch *SaveBatch, encoder *valueEncoder) (string, error) {
	copy := *batch
	copy.BatchId = ""
	// Encode the complete typed representation. A numeric or custom payload type can change
	// meaning even when ordinary JSON would produce the same text.
	data, err := encoder.marshal(&copy)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func registryOrDefault(registries []*CodecRegistry) *CodecRegistry {
	if len(registries) > 0 && registries[0] != nil {
		return registries[0]
	}
	return NewCodecRegistry()
}
