package gent

import (
	core "github.com/rickchristie/gent/internal/core"
)

// CodecLimits bounds saved records, logical expansion, and decoded binary buffers.
// Zero leaves encoded and binary sizes unlimited. Zero expansion limits select 256 MiB and
// 4,194,304 JSON values. Positive values replace these defaults. Expansion counts every reference
// occurrence before constructing Go values, including saved fields absent from current code.
// MaxExpandedBytes also bounds decompressed storage tables before their allocation.
// It bounds each derived array type and reflective value allocation. These are separate checks,
// not a total heap limit; container overhead and custom decoder allocations can add memory use.
// MaxEncodedBytes also applies to component envelopes and uncompressed pending event documents.
// Custom codecs must enforce their own decoded allocation limits for application-specific data.
type CodecLimits = core.CodecLimits

// CodecOption configures a registry before it is shared by executions.
type CodecOption = core.CodecOption

func WithCodecLimits(limits CodecLimits) CodecOption {
	return core.WithCodecLimits(limits)
}
