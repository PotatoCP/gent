package gent

import (
	core "github.com/rickchristie/gent/internal/core"
)

// Loop performs one iteration and defines the typed output contract. Use native model completion
// to select tools, continuation, or Output processing. AcceptOutput prepares a validated success;
// the runner commits that value and its delivery content before returning it to the caller.
type Loop[T any] = core.Loop[T]

// TurnResetter clears author-declared turn fields before NewTurn accepts input. Use local
// assignments only: do not call models, tools, or external services. External work before the
// admission save could repeat after failure. Restore and Continue do not call ResetTurn.
type TurnResetter = core.TurnResetter

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
type LoopData = core.LoopData

// BasicLoopData supplies an empty local-state contract. Config uses it by default. Task input
// belongs to ExecutionContext. A custom struct that embeds BasicLoopData must implement complete
// capture and restoration for its own fields; embedding does not serialize those added fields.
type BasicLoopData = core.BasicLoopData

// NewBasicLoopData creates task-independent data for a new context.
func NewBasicLoopData() *BasicLoopData {
	return core.NewBasicLoopData()
}

// RecordOrigin indicates how a HistoryEntry was created: an original observation, a summary,
// a replacement derived from earlier content, retrieved history, or a system injection.
type RecordOrigin = core.RecordOrigin

// RecordUserInput identifies caller-authored conversation input accepted by NewTurn.
// Role alone does not establish user origin: generated corrections can also use a user role.
const RecordUserInput RecordOrigin = core.RecordUserInput

// RecordOriginal is the zero value. The record was
// created normally by the Loop during execution.
const RecordOriginal RecordOrigin = core.RecordOriginal

// RecordCompactedSynthetic means this record summarizes earlier records, which may span
// multiple executor iterations.
const RecordCompactedSynthetic RecordOrigin = core.RecordCompactedSynthetic

// RecordCompactedModified means this record was derived by modifying earlier content.
// The replacement gets a new history entry; the original entry remains unchanged.
const RecordCompactedModified RecordOrigin = core.RecordCompactedModified

// RecordRetrievedHistory means this record was retrieved from earlier history for reuse
// in working context (e.g., via a retrieval-augmented strategy).
const RecordRetrievedHistory RecordOrigin = core.RecordRetrievedHistory

// RecordSystemInjected means this record was injected
// by a system hook (e.g., pre-search policy suggestions).
// It is not produced by the agent or user.
const RecordSystemInjected RecordOrigin = core.RecordSystemInjected

// RecordMetadataKey is a typed key for HistoryEntry metadata.
// The framework defines standard keys (IMK* constants). Users
// can define their own keys with a custom prefix.
type RecordMetadataKey = core.RecordMetadataKey

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
const IMKImportanceScore RecordMetadataKey = core.IMKImportanceScore

// ImportanceScorePinned is the minimum importance score for
// a record to be considered "pinned" by the standard
// compaction strategies. Standard compaction keeps pinned non-user records selected. Eligible
// user input follows its separate retention allowance, including pinned user records. Original
// entries remain in the full archive regardless of their score.
//
// The standard strategies use: score >= ImportanceScorePinned.
const ImportanceScorePinned float64 = core.ImportanceScorePinned

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
type HistoryEntry = core.HistoryEntry

// GetImportanceScore is a convenience function that returns
// the importance score for a record.
// Returns (0, false) if the key is absent or not a float64.
func GetImportanceScore(iter *HistoryEntry) (float64, bool) {
	return core.GetImportanceScore(iter)
}

// MessageContentMetadataKey is a typed key for MessageContent
// metadata. Framework keys use the "gent:" prefix (MMK*
// constants). Users may define custom keys in their own
// namespace.
type MessageContentMetadataKey = core.MessageContentMetadataKey

// MMKToolCallRecords stores []*ToolCallRecord as private execution evidence on a message.
// Only the prepared responses in ModelMessage.Parts are sent to the model.
const MMKToolCallRecords MessageContentMetadataKey = core.MMKToolCallRecords

// MessageContent is wrapper around [ModelMessage] used
// in Loop.
type MessageContent = core.MessageContent

// Task supplies new conversation input and an optional replacement system prefix. Nil Instructions
// inherits the accepted prefix; a non-nil empty slice clears it. Replacing the prefix can increase
// input work and latency when a provider cannot reuse its prompt cache. No cache hit is guaranteed.
// Messages must include a user message and must not contain system instructions. Gent freezes
// input at admission; later requests use the prefix and selected history, not the whole Task again.
// Assistant content and complete tool request/reply groups can supply prior conversation. Gent
// keeps each tool group in one history record so record selection cannot separate its replies.
type Task = core.Task

// NewTask creates one user message and inherits the accepted system prefix. Use Task.Instructions
// to supply or replace system messages. Output instructions apply only when the caller supplies
// them.
func NewTask(parts ...ContentPart) *Task {
	return core.NewTask(parts...)
}

type LoopAction = core.LoopAction

// LAContinue asks the Runner to save the completed iteration and enter the next iteration.
const LAContinue LoopAction = core.LAContinue

// LATerminate asks the Runner to accept the supplied completed-turn outcome.
const LATerminate LoopAction = core.LATerminate

type AgentLoopResult = core.AgentLoopResult
