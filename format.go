package gent

import (
	core "github.com/rickchristie/gent/internal/core"
)

// FormattedSection is a named block in tool results or summaries. A renderer can include its
// children recursively. These blocks never identify or delimit the model's answer.
type FormattedSection = core.FormattedSection

// TextFormat renders text supplied by Gent or a tool. It has no parsing or prompt registration
// methods. Output processes complete model candidates through their native completion state.
type TextFormat = core.TextFormat
