package gent

import (
	core "github.com/rickchristie/gent/internal/core"
)

// TextPart supplies exact authored text. Gent does not parse it as JSON, even when it looks
// like structured data. Use DataPart when Gent must capture and encode a Go value.
func TextPart(text string) ContentPart {
	return core.TextPart(text)
}

// BytesPart supplies a file and its MIME type without conversion or format detection.
// The part takes ownership of data; do not modify the buffer after submission. Gent saves
// its bytes for recovery. Provider support is checked at delivery, with safe steering when
// unsupported. Direct tool responses include bytes; PTC requires same-script tool.forward.
func BytesPart(mimeType string, data []byte) ContentPart {
	return core.BytesPart(mimeType, data)
}

// ImagePart detects a supported image MIME type without changing the bytes. It returns an
// error for unknown formats. On success, the ownership and recovery rules of BytesPart apply.
func ImagePart(data []byte) (ContentPart, error) {
	return core.ImagePart(data)
}

// PNGPart supplies PNG bytes. It does not validate or convert the file; see BytesPart ownership.
func PNGPart(data []byte) ContentPart {
	return core.PNGPart(data)
}

// JPEGPart supplies JPEG bytes. It does not convert the file; see BytesPart ownership.
func JPEGPart(data []byte) ContentPart {
	return core.JPEGPart(data)
}

// GIFPart supplies GIF bytes. Provider animation support can differ; see BytesPart ownership.
func GIFPart(data []byte) ContentPart {
	return core.GIFPart(data)
}

// WebPPart supplies WebP bytes without conversion. See BytesPart for ownership and delivery.
func WebPPart(data []byte) ContentPart {
	return core.WebPPart(data)
}

// PDFPart supplies a PDF file without extracting text. See BytesPart for ownership and delivery.
func PDFPart(data []byte) ContentPart {
	return core.PDFPart(data)
}

// AudioPart supplies audio bytes with the caller's MIME type. It does not transcode audio.
// Provider support is checked at delivery. The ownership and recovery rules of BytesPart apply.
func AudioPart(mimeType string, data []byte) ContentPart {
	return core.AudioPart(mimeType, data)
}

// VideoPart supplies video bytes with the caller's MIME type. It does not transcode video.
// Provider support is checked at delivery. The ownership and recovery rules of BytesPart apply.
func VideoPart(mimeType string, data []byte) ContentPart {
	return core.VideoPart(mimeType, data)
}

// PartsBuilder captures ordered content for messages, tool responses, or recovery evidence.
// Data is captured when added. Byte buffers transfer ownership without a defensive copy.
// Build reports all construction errors together and returns no partial content on failure.
// A builder belongs to one goroutine and must not be changed after Build.
type PartsBuilder = core.PartsBuilder

// NewParts starts an empty content list. Provider compatibility is checked after construction.
func NewParts() *PartsBuilder {
	return core.NewParts()
}

// ValidateContentParts rejects native call traffic inside tool content. Cached content retains
// the same restriction. This checks the framework contract, not the author's output schema.
func ValidateContentParts(parts []ContentPart) error {
	return core.ValidateContentParts(parts)
}

// ContentTextData renders text and structured data for text-only destinations and summaries.
// It does not flatten media. Callers must handle media separately instead of silently dropping it.
func ContentTextData(parts []ContentPart) string {
	return core.ContentTextData(parts)
}
