package gent

import (
	"time"
)

// Loop performs one iteration and defines the typed output contract. Use native model completion
// to select tools, continuation, or Output processing. AcceptOutput prepares a validated success;
// the runner commits that value and its delivery content before returning it to the caller.
type Loop[T any] interface {
	Next(execCtx *ExecutionContext) (*AgentLoopResult, error)
	Output() Output[T]
}

// TurnResetter clears author-declared turn fields before NewTurn accepts input. Use local
// assignments only: do not call models, tools, or external services. External work before the
// admission save could repeat after failure. Restore and Continue do not call ResetTurn.
type TurnResetter interface {
	ResetTurn()
}

// LoopData owns serializable per-run application progress. Every approved savepoint includes
// complete custom data and the matching framework continuation. SaveCheckpointState must supply
// stable owned values. RestoreCheckpointState borrows its input to restore a fresh factory value.
// Runtime clients and contexts are bindings, not saved logical data.
//
// Capture all added fields explicitly. Embedding BasicLoopData does not serialize application
// fields or reset added turn fields. A promoted base serializer cannot discover those fields.
// NewTurn resets author-declared turn fields only; preserve run fields across turns.
//
// Evolve saved data with compatible additions. Preserve field identities, types, units, and
// meanings. Add a field for a new representation; retain old fields and reader behavior while
// saved runs need them. New fields need valid defaults or values derived from saved facts.
// Distinguish absent data from explicit zero when required. Never reset accepted progress merely
// because a new field is absent. Reflective encoding uses Go field names; JSON tags do not preserve
// a renamed Go field. Keep saved type IDs and versions stable for compatible additions.
//
// If custom code uses steps or enums, preserve old identities and meanings and add new values.
// Gent does not require a step enum. After restore, Next restarts at its entry point on restored
// data; its author owns repeated local work and new operations. Managed calls reuse accepted
// results for the same operation. No automatic Go-stack continuation or migration API is provided.
type LoopData interface {
	CheckpointState
	TurnResetter
}

// BasicLoopData supplies an empty local-state contract. Config uses it by default. Task input
// belongs to ExecutionContext. A custom struct that embeds BasicLoopData must implement complete
// capture and restoration for its own fields; embedding does not serialize those added fields.
type BasicLoopData struct{}

// NewBasicLoopData creates task-independent data for a new context.
func NewBasicLoopData() *BasicLoopData { return &BasicLoopData{} }

// RecordOrigin indicates how a HistoryEntry was created: an original observation, a summary,
// a replacement derived from earlier content, retrieved history, or a system injection.
type RecordOrigin string

const (
	// RecordOriginal is the zero value. The record was
	// created normally by the Loop during execution.
	RecordOriginal RecordOrigin = ""

	// RecordUserInput identifies caller-authored conversation input accepted by NewTurn.
	// Role alone does not establish user origin: generated corrections can also use a user role.
	RecordUserInput RecordOrigin = "user_input"

	// RecordCompactedSynthetic means this record summarizes earlier records, which may span
	// multiple executor iterations.
	RecordCompactedSynthetic RecordOrigin = "compacted_synthetic"

	// RecordCompactedModified means this record was derived by modifying earlier content.
	// The replacement gets a new history entry; the original entry remains unchanged.
	RecordCompactedModified RecordOrigin = "compacted_modified"

	// RecordRetrievedHistory means this record was retrieved from earlier history for reuse
	// in working context (e.g., via a retrieval-augmented strategy).
	RecordRetrievedHistory RecordOrigin = "retrieved_history"

	// RecordSystemInjected means this record was injected
	// by a system hook (e.g., pre-search policy suggestions).
	// It is not produced by the agent or user.
	RecordSystemInjected RecordOrigin = "system_injected"
)

// RecordMetadataKey is a typed key for HistoryEntry metadata.
// The framework defines standard keys (IMK* constants). Users
// can define their own keys with a custom prefix.
type RecordMetadataKey string

// IMKImportanceScore is a float64 value in [-10, 10] that
// indicates how important this record is.
//
// Positive values = more important (prefer to keep).
// Negative values = less important (prefer to discard).
// Zero or absent = neutral (default treatment).
//
// The standard strategies treat any score >=
// [ImportanceScorePinned] (10.0) as "pinned" and preserve
// those records in the active selection through compaction.
//
// Usage:
//
//	// Keep this record in the active selection through compaction:
//	record.SetMetadata(
//	    gent.IMKImportanceScore,
//	    gent.ImportanceScorePinned,
//	)
//
//	score, ok := gent.GetImportanceScore(record)
const IMKImportanceScore RecordMetadataKey = "gent:importance_score"

// ImportanceScorePinned is the minimum importance score for
// a record to be considered "pinned" by the standard
// compaction strategies. Standard compaction keeps pinned non-user records selected. Eligible
// user input follows its separate retention allowance, including pinned user records. Original
// entries remain in the full archive regardless of their score.
//
// The standard strategies use: score >= ImportanceScorePinned.
const ImportanceScorePinned float64 = 10.0

// HistoryEntry preserves a message group, a summary, or a compaction decision with stable identity.
// Message entries are selected, expired, and compacted together. One Next call can produce several
// entries, and a summary can represent several iterations. A compaction decision stores provenance
// in Compaction instead of Messages. Iteration counters and limits count Next calls, never entries.
//
// Execution owns accepted entries. Getters, Recorder, and Subscriber callbacks expose borrowed,
// read-only values. Use UpdateHistory to append a replacement. The archive keeps the original.
//
// Messages must never contain nil elements. All code that
// constructs or modifies a HistoryEntry must ensure every
// element in Messages is a valid, non-nil pointer.
type HistoryEntry struct {
	Id          EntryId           `json:"id"`
	Order       uint64            `json:"order"`
	RunId       string            `json:"runId"`
	ContextId   string            `json:"contextId"`
	Iteration   int               `json:"iteration"`
	AttemptId   string            `json:"attemptId"`
	Kind        HistoryEntryKind  `json:"kind"`
	ModelCallId string            `json:"modelCallId,omitempty"`
	ToolCallIds []string          `json:"toolCallIds,omitempty"`
	Compaction  *CompactionRecord `json:"compaction,omitempty"`
	Replaces    []EntryId         `json:"replaces,omitempty"`
	CreatedAt   time.Time         `json:"createdAt"`

	Messages []*MessageContent

	// Origin indicates how this record was created.
	// Zero value (RecordOriginal) means it was created
	// normally by the Loop.
	Origin RecordOrigin

	// ExpireAfterIteration is the executor iteration number
	// at which this record expires from working context. Zero (default) means
	// no expiry. Since executor iterations are 1-indexed,
	// zero is unambiguous.
	//
	// When set to a positive value, the record is
	// included when execCtx.Iteration() < ExpireAfterIteration
	// and skipped when execCtx.Iteration() >= ExpireAfterIteration.
	//
	// Example: ExpireAfterIteration = 3 means the record
	// is included in iterations 1 and 2, and skipped from
	// iteration 3 onward. Its original history entry remains available.
	ExpireAfterIteration int

	// Metadata contains optional key-value pairs for this
	// record. The framework defines standard keys (IMK*
	// constants); users can add custom keys.
	//
	// This field is exported for JSON marshalling. Use
	// SetMetadata/GetMetadata helper methods for access
	// instead of writing to the map directly.
	//
	// This field is nil by default and lazily initialized on
	// first write. Always use SetMetadata/GetMetadata helper
	// methods instead of accessing the map directly to avoid
	// nil map panics.
	//
	//   // WRONG — panics if Metadata is nil:
	//   iter.Metadata[gent.IMKImportanceScore] = 5.0
	//
	//   // CORRECT — safe, initializes map if needed:
	//   iter.SetMetadata(gent.IMKImportanceScore, 5.0)
	//
	//   // CORRECT — safe, returns zero value + false:
	//   val, ok := iter.GetMetadata(gent.IMKImportanceScore)
	Metadata map[RecordMetadataKey]any
}

// SetMetadata sets a metadata value, initializing the map
// if nil.
func (i *HistoryEntry) SetMetadata(
	key RecordMetadataKey,
	value any,
) {
	if i.Metadata == nil {
		i.Metadata = make(map[RecordMetadataKey]any)
	}
	i.Metadata[key] = value
}

// GetMetadata returns a metadata value and whether it was
// present. Returns (nil, false) if Metadata is nil or the
// key is absent.
func (i *HistoryEntry) GetMetadata(
	key RecordMetadataKey,
) (any, bool) {
	if i.Metadata == nil {
		return nil, false
	}
	val, ok := i.Metadata[key]
	return val, ok
}

// GetImportanceScore is a convenience function that returns
// the importance score for a record.
// Returns (0, false) if the key is absent or not a float64.
func GetImportanceScore(iter *HistoryEntry) (float64, bool) {
	val, ok := iter.GetMetadata(IMKImportanceScore)
	if !ok {
		return 0, false
	}
	score, ok := val.(float64)
	return score, ok
}

// MessageContentMetadataKey is a typed key for MessageContent
// metadata. Framework keys use the "gent:" prefix (MMK*
// constants). Users may define custom keys in their own
// namespace.
type MessageContentMetadataKey string

// MMKToolCallRecords stores []*ToolCallRecord as private execution evidence on a message.
// Only the prepared responses in ModelMessage.Parts are sent to the model.
const MMKToolCallRecords MessageContentMetadataKey = "gent:tool_call_records"

// MessageContent is wrapper around [ModelMessage] used
// in Loop.
type MessageContent struct {
	Continuation *ProviderContinuation
	Role         ChatMessageType
	Parts        []ContentPart

	// Metadata contains optional key-value pairs for this
	// message. The framework defines standard keys (MMK*
	// constants); users can add custom keys.
	//
	// This field is nil by default and lazily initialized on
	// first write. Always use SetMetadata/GetMetadata helper
	// methods instead of accessing the map directly to avoid
	// nil map panics.
	Metadata map[MessageContentMetadataKey]any
}

// SetMetadata sets a metadata value, initializing the map
// if nil.
func (m *MessageContent) SetMetadata(
	key MessageContentMetadataKey,
	value any,
) {
	if m.Metadata == nil {
		m.Metadata = make(map[MessageContentMetadataKey]any)
	}
	m.Metadata[key] = value
}

// GetMetadata returns a metadata value and whether it was
// present. Returns (nil, false) if Metadata is nil or the
// key is absent.
func (m *MessageContent) GetMetadata(
	key MessageContentMetadataKey,
) (any, bool) {
	if m.Metadata == nil {
		return nil, false
	}
	val, ok := m.Metadata[key]
	return val, ok
}

// Task supplies new conversation input and an optional replacement system prefix. Nil Instructions
// inherits the accepted prefix; a non-nil empty slice clears it. Replacing the prefix can increase
// input work and latency when a provider cannot reuse its prompt cache. No cache hit is guaranteed.
// Messages must include a user message and must not contain system instructions. Gent freezes
// input at admission; later requests use the prefix and selected history, not the whole Task again.
// Assistant content and complete tool request/reply groups can supply prior conversation. Gent
// keeps each tool group in one history record so record selection cannot separate its replies.
type Task struct {
	Instructions []*ModelMessage
	Messages     []*ModelMessage
}

// NewTask creates one user message and inherits the accepted system prefix. Use Task.Instructions
// to supply or replace system messages. Output instructions apply only when the caller supplies
// them.
func NewTask(parts ...ContentPart) *Task {
	return &Task{Messages: []*ModelMessage{{Role: ChatMessageTypeHuman, Parts: parts}}}
}

type LoopAction string

const (
	// LAContinue asks the Runner to save the completed iteration and enter the next iteration.
	LAContinue LoopAction = "c"
	// LATerminate asks the Runner to accept the supplied completed-turn outcome.
	LATerminate LoopAction = "t"
)

type AgentLoopResult struct {
	// Value is the encoded typed result produced by AcceptOutput. It remains separate from
	// delivery content so restoration does not repeat parsing or domain validation.
	Value             *EncodedValue
	TerminationReason TerminationReason
	Refusal           *ModelRefusal
	// Action indicates whether to continue or terminate the loop.
	// The [Loop] is completely free to determine whether to continue or terminate.
	Action LoopAction

	// Result is only set when Action is [LATerminate].
	// This is a slice of ContentPart to support multimodal outputs.
	Result []ContentPart

	// ContentSource optionally identifies the provisional messages replaced by this result.
	ContentSource *ContentSource
}
