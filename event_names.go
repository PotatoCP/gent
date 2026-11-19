package gent

import (
	core "github.com/rickchristie/gent/internal/core"
)

// EventNameStateChange identifies a structured history selection update.
const EventNameStateChange = core.EventNameStateChange

// Execution lifecycle
const EventNameExecutionBefore = core.EventNameExecutionBefore

const EventNameExecutionAfter = core.EventNameExecutionAfter

// Iteration lifecycle
const EventNameIterationBefore = core.EventNameIterationBefore

const EventNameIterationAfter = core.EventNameIterationAfter

// Model calls
const EventNameModelCallBefore = core.EventNameModelCallBefore

const EventNameModelCallAfter = core.EventNameModelCallAfter

// Tool calls
const EventNameToolCallBefore = core.EventNameToolCallBefore

const EventNameToolCallAfter = core.EventNameToolCallAfter

// Errors and validation
const EventNameParseError = core.EventNameParseError

const EventNameError = core.EventNameError

// Limits
const EventNameLimitExceeded = core.EventNameLimitExceeded

// Compaction
const EventNameCompaction = core.EventNameCompaction

// ParseErrorType categorizes the source of a parse error.
type ParseErrorType = core.ParseErrorType

// ParseErrorTypeToolchain identifies invalid native calls or JSON tool arguments.
const ParseErrorTypeToolchain ParseErrorType = core.ParseErrorTypeToolchain
