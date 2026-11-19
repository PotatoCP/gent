package gent

import (
	core "github.com/rickchristie/gent/internal/core"
)

type EntryId = core.EntryId

type HistoryRevision = core.HistoryRevision

type HistoryEntryKind = core.HistoryEntryKind

type ContextChangeKind = core.ContextChangeKind

// HistoryMessages stores an immutable group of conversation messages.
const HistoryMessages HistoryEntryKind = core.HistoryMessages

// HistorySummary stores a summary derived from earlier active context.
const HistorySummary HistoryEntryKind = core.HistorySummary

// HistoryCompaction stores the inputs and selection of an accepted compaction.
const HistoryCompaction HistoryEntryKind = core.HistoryCompaction

// ContextAppend adds records to the active context.
const ContextAppend ContextChangeKind = core.ContextAppend

// ContextReplace replaces the selected context with the proposed ordered entries.
const ContextReplace ContextChangeKind = core.ContextReplace

// ContextReorder changes active entry order without editing saved originals.
const ContextReorder ContextChangeKind = core.ContextReorder

// ContextExpire removes expired entries from the active context.
const ContextExpire ContextChangeKind = core.ContextExpire

// ContextCompact accepts a summary and a compacted context selection.
const ContextCompact ContextChangeKind = core.ContextCompact

// ContextCustom records a custom state or selection transition.
const ContextCustom ContextChangeKind = core.ContextCustom

type CompactionRecord = core.CompactionRecord

type ContextSelection = core.ContextSelection

// StateValue replaces one named application value. It is structured data, not a text diff.
type StateValue = core.StateValue

type HistoryChange = core.HistoryChange

// HistoryUpdate proposes one context change. Compaction supplies its provenance here;
// the executor fills the decision identity and commits it with the trigger and checkpoint.
type HistoryUpdate = core.HistoryUpdate

type ContextDraft = core.ContextDraft

// HistorySnapshot includes every entry and selection transition; compaction never removes evidence.
type HistorySnapshot = core.HistorySnapshot
