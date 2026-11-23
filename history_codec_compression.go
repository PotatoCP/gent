package gent

import core "github.com/rickchristie/gent/internal/core"

// CodecCompression selects lossless storage compression. It does not change batch receipts,
// content types, native bytes, or logical expansion limits. Decoding accepts both formats.
type CodecCompression = core.CodecCompression

const (
	// CodecCompressionNone keeps the complete JSON tables readable without decompression.
	CodecCompressionNone = core.CodecCompressionNone
	// CodecCompressionSnappy compresses large documents when it saves at least one eighth.
	// This is the default. Each document has its own dictionary and remains valid JSON.
	CodecCompressionSnappy = core.CodecCompressionSnappy
)

// WithCodecCompression sets storage policy before a registry is shared. The default uses
// Snappy for documents of at least 8 KiB. Small or incompressible documents keep plain tables.
func WithCodecCompression(compression CodecCompression) CodecOption {
	return core.WithCodecCompression(compression)
}
