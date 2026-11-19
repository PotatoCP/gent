package gent

import (
	core "github.com/rickchristie/gent/internal/core"
)

// CodecLimits bounds a saved record and each decoded binary buffer. Zero means no limit.
// Custom codecs must enforce their own decoded allocation limits for application-specific data.
type CodecLimits = core.CodecLimits

// CodecOption configures a registry before it is shared by executions.
type CodecOption = core.CodecOption

func WithCodecLimits(limits CodecLimits) CodecOption {
	return core.WithCodecLimits(limits)
}
