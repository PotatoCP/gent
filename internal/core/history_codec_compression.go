package gent

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/golang/snappy"
)

// CodecCompression selects lossless storage compression. It does not change batch receipts,
// content types, native bytes, or logical expansion limits. Decoding accepts both formats.
type CodecCompression string

const (
	// CodecCompressionNone keeps the complete JSON tables readable without decompression.
	CodecCompressionNone CodecCompression = "none"
	// CodecCompressionSnappy compresses large documents when it saves at least one eighth.
	// This is the default. Each document has its own dictionary and remains valid JSON.
	CodecCompressionSnappy CodecCompression = "snappy"
)

// WithCodecCompression sets storage policy before a registry is shared. The default uses
// Snappy for documents of at least 8 KiB. Small or incompressible documents keep plain tables.
func WithCodecCompression(compression CodecCompression) CodecOption {
	if compression != CodecCompressionNone && compression != CodecCompressionSnappy {
		panic("gent: invalid codec compression")
	}
	return func(registry *CodecRegistry) { registry.compression = compression }
}

func (registry *CodecRegistry) canCompressDocument(size int) bool {
	limit, _ := registry.expandedLimits()
	return registry.compression == CodecCompressionSnappy && size >= 8*1024 && size <= limit
}

func (registry *CodecRegistry) compressDocument(data []byte) (int, []byte) {
	if !registry.canCompressDocument(len(data)) {
		return savedDocumentVersion, data
	}
	compressed := snappy.Encode(nil, data)
	size := 2 + base64.StdEncoding.EncodedLen(len(compressed))
	// Include base64 and JSON quoting in the decision. Media that is already compressed must
	// not grow in storage. Do not pay an extra retained buffer cost for a small byte reduction.
	plainSize := documentEnvelopeSize(savedDocumentVersion) + len(data)
	storedSize := documentEnvelopeSize(compressedDocumentVersion) + size
	if storedSize > plainSize-plainSize/8 {
		return savedDocumentVersion, data
	}
	wire := make([]byte, size)
	wire[0], wire[size-1] = '"', '"'
	base64.StdEncoding.Encode(wire[1:size-1], compressed)
	return compressedDocumentVersion, wire
}

func (registry *CodecRegistry) documentData(
	version int, data savedJSONValue,
) (savedJSONValue, error) {
	if version == savedDocumentVersion {
		return data, nil
	}
	var text string
	if err := tableString(data, &text); err != nil {
		return savedJSONValue{}, err
	}
	if strings.ContainsAny(text, "\r\n") {
		return savedJSONValue{}, errors.New("gent: compressed document must use canonical base64")
	}
	compressed, err := base64.StdEncoding.Strict().DecodeString(text)
	if err != nil {
		return savedJSONValue{}, fmt.Errorf("gent: compressed document base64: %w", err)
	}
	size, err := snappy.DecodedLen(compressed)
	if err != nil {
		return savedJSONValue{}, fmt.Errorf("gent: compressed document size: %w", err)
	}
	limit, _ := registry.expandedLimits()
	// Check the declared size before allocating the uncompressed tables. Logical expansion
	// is then checked separately before any application codec or typed value construction.
	if size > limit {
		return savedJSONValue{}, fmt.Errorf("gent: decompressed document exceeds %d bytes", limit)
	}
	decoded, err := snappy.Decode(nil, compressed)
	if err != nil {
		return savedJSONValue{}, fmt.Errorf("gent: decompress document: %w", err)
	}
	return indexSavedJSON(decoded)
}
