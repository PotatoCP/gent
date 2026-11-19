package gent

import (
	core "github.com/rickchristie/gent/internal/core"
)

// RunSchemaVersion identifies the saved format used by SaveBatch, RunView, and Checkpoint.
// Batch application and recovery require this version; it is separate from custom codec versions.
const RunSchemaVersion = core.RunSchemaVersion

// RecordedEvent carries ordered execution evidence with a concrete, frozen payload.
// Consumers MUST NOT modify this envelope or any value reachable through its fields.
// Read fields only, including nested payload pointers, slices, maps, and byte buffers.
// Deduplicate Live previews by RunId, AttemptId, and EventSequenceNumber.
// Deduplicate Accepted events by RunId and EventSequenceNumber. A failed OnEvent can have sent
// data already; a delivery cursor is not a browser acknowledgment. Unsaved positions can be reused
// by another attempt. EventSequenceNumber defines order; Ts is for display and diagnostics.
type RecordedEvent = core.RecordedEvent

// RuntimeState preserves accepted accounting and identity allocation for the whole run.
// Approved saves accept it with the matching control checkpoint. Recovery restores these totals
// without recounting usage or allocating an identity that accepted work already used.
type RuntimeState = core.RuntimeState

// ContextRuntime is one context's accounting at a save boundary. Counters and gauges are absolute
// values, not changes to replay. The context's control state is stored in ContextCheckpoint.
type ContextRuntime = core.ContextRuntime

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
type SaveBatch = core.SaveBatch

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
type Recorder = core.Recorder

// RunView reconstructs the complete accepted archive from stored SaveBatch values. Apply advances
// its cursor and contents together without executing models, tools, Hooks, or output processing.
// It preserves original prompts, native and readable evidence, and compaction changes. It is an
// inspection view; use RunState for execution restoration. A compact RunStateSnapshot cannot
// replace earlier batches when the application needs the full archive.
type RunView = core.RunView

// ErrEventGap means the expected saved cursor or consecutive event sequence does not match.
// An empty SaveBatch.Events also fails this requirement.
var ErrEventGap = core.ErrEventGap

// ErrWrongRun means a batch or event does not belong to the view's logical run.
var ErrWrongRun = core.ErrWrongRun

// ErrHistoryConflict means a history transition conflicts with accepted history state.
var ErrHistoryConflict = core.ErrHistoryConflict

// ErrPersistenceStopped means new acceptance was attempted while an earlier save was
// unresolved. Reconcile that exact request before resuming execution on a fresh context.
var ErrPersistenceStopped = core.ErrPersistenceStopped

// PersistenceError retains the exact uncertain batch. Do not issue new external work until the
// application reconciles this batch with storage or restores the last accepted run.
type PersistenceError = core.PersistenceError

// BatchDigest includes typed values, so distinct numeric or custom content types cannot share
// an identity merely because ordinary JSON would render them alike.
// It returns a SHA-256 hexadecimal digest of the request with BatchId cleared. Supply the run's
// codec registry for custom types; omitting it uses the default registry. Gent uses this digest
// as BatchId, and Apply recomputes it before accepting a new request or an exact duplicate.
func BatchDigest(batch *SaveBatch, registries ...*CodecRegistry) (string, error) {
	return core.BatchDigest(batch, registries...)
}
