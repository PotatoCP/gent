package gent

import core "github.com/rickchristie/gent/internal/core"

// UserMessageRetentionStrategy proposes retained user input without changing execution. Retain
// must not mutate its borrowed input, write history, reset counters, call models, or do external
// work. The compactor combines the proposal and summary at one accepted compaction boundary.
type UserMessageRetentionStrategy = core.UserMessageRetentionStrategy

// UserMessageRetentionEntry identifies the original user message and its current projection.
// Truncated remains true after any earlier shortening. Do not reload omitted text from the archive.
// Message contains only user content. The compactor adds the separate notice for delivery.
type UserMessageRetentionEntry = core.UserMessageRetentionEntry

// UserMessageRetentionInput is borrowed read-only. Messages are ordered from oldest to newest
// and contain saved user-authored input, not generated guidance that happens to have a human role.
// MaxBytes measures content bytes, not tokens or encoded message size. Marker is the complete
// effective omission notice; EarlierContentOmitted carries omission across previous compactions.
type UserMessageRetentionInput = core.UserMessageRetentionInput

// UserMessageRetentionResult preserves source order and identity. RetainedBytes includes one
// complete Marker when EarlierContentOmitted is true, even though Messages contains only user
// content. Returning fewer messages or none is valid. Going over MaxBytes is a strategy error.
type UserMessageRetentionResult = core.UserMessageRetentionResult

// UserMessageRetentionConfig belongs to the standard summarizer. Zero MaxBytes selects 32 KiB;
// negative values are invalid. An empty marker selects the framework notice. Nil Strategy selects
// newest-message retention with UTF-8 suffix truncation at the boundary. There is no user-message
// protection or unconditional NewTurn trimming; this policy runs only when compaction runs.
type UserMessageRetentionConfig = core.UserMessageRetentionConfig
