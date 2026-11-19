package gent

import (
	core "github.com/rickchristie/gent/internal/core"
)

// Config supplies shared services and execution policy. Set it before creating or restoring a
// context, then treat it and its reachable values as read-only. Per-run progress belongs in
// ExecutionContext and LoopData. Shared callbacks and services own their concurrency contracts.
type Config = core.Config

// RecordingConfig bounds unsaved event evidence. These are hard allowances, not save triggers.
// Zero fields use defaults. Negative values are invalid. A failed or uncertain Save requires
// storage reconciliation and accepted-state restoration before another execution attempt.
type RecordingConfig = core.RecordingConfig

// Hooks can change effective operation inputs before Gent freezes them. Both callbacks are
// optional and run synchronously. Return an error to prevent the dependent operation. Do not
// change operation identities, accepted history, or the saved task. Compose multiple changes in
// one callback in an explicit order. Callbacks can repeat after interruption; use managed tools
// for external actions that need recovery. Do not keep mutable access after returning.
type Hooks = core.Hooks
