package gent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

// Documents share wire values, never mutable Go objects. The tables belong to one document;
// loading a saved value cannot depend on a process dictionary or an earlier archive file.
const savedDocumentVersion = 1

const (
	maxTableDepth         = 512
	defaultExpandedBytes  = 256 << 20
	defaultExpandedValues = 4 << 20
)

type tableNodeKind string

const (
	// tableRecord identifies a record whose numeric tag is its layout index.
	tableRecord tableNodeKind = "record"
	// tableArray identifies an ordered list of saved values.
	tableArray tableNodeKind = "array"
	// tableReference identifies a use of an earlier shared value.
	tableReference tableNodeKind = "reference"
	// tableLiteral identifies opaque component JSON without table interpretation.
	tableLiteral tableNodeKind = "literal"
)

// String kinds keep internal control flow explicit. Negative integer tags belong only to the
// saved grammar. Nonnegative tags are record layout indexes, not additional enum values.
func tableWireTag(kind tableNodeKind) int {
	switch kind {
	case tableArray:
		return -1
	case tableReference:
		return -2
	case tableLiteral:
		return -3
	default:
		panic("gent: a record tag requires its layout index")
	}
}

func tableKind(tag int) (tableNodeKind, error) {
	if tag >= 0 {
		return tableRecord, nil
	}
	switch tag {
	case -1:
		return tableArray, nil
	case -2:
		return tableReference, nil
	case -3:
		return tableLiteral, nil
	default:
		return "", errors.New("gent: invalid layout reference")
	}
}

type savedDocument struct {
	FormatVersion int             `json:"formatVersion"`
	Data          json.RawMessage `json:"data"`
	Digest        string          `json:"digest"`
}

type savedDocumentData struct {
	Types   []string          `json:"types"`
	Layouts [][][]any         `json:"layouts"`
	Shared  []json.RawMessage `json:"shared"`
	Root    json.RawMessage   `json:"root"`
}

// Include the format marker in the digest. This detects changed storage bytes, not a writer
// with permission to replace both the data and its digest. Storage trust remains with the app.
func documentDigest(data []byte) string {
	hash := sha256.New()
	hash.Write([]byte("gent-saved-document:1\n"))
	hash.Write(data)
	return hex.EncodeToString(hash.Sum(nil))
}

type tableMeasure struct {
	bytes  int
	values int
	depth  int
}

func (r *CodecRegistry) expandedLimits() (int, int) {
	bytes, values := r.limits.MaxExpandedBytes, r.limits.MaxExpandedValues
	if bytes == 0 {
		bytes = defaultExpandedBytes
	}
	if values == 0 {
		values = defaultExpandedValues
	}
	return bytes, values
}

func (r *CodecRegistry) checkExpansion(size tableMeasure) error {
	bytes, values := r.expandedLimits()
	if size.bytes > bytes {
		return fmt.Errorf("gent: expanded value exceeds %d bytes", bytes)
	}
	if size.values > values {
		return fmt.Errorf("gent: expanded value exceeds %d values", values)
	}
	if size.depth > maxTableDepth {
		return fmt.Errorf("gent: saved document nesting exceeds %d", maxTableDepth)
	}
	return nil
}

// Check before adding to avoid integer overflow and before allocating decoded Go values.
// Repeated references contribute their full logical size at every use, including ignored fields.
func (r *CodecRegistry) addExpansion(total *tableMeasure, child tableMeasure) error {
	bytes, values := r.expandedLimits()
	if child.bytes > bytes-total.bytes {
		return fmt.Errorf("gent: expanded value exceeds %d bytes", bytes)
	}
	if child.values > values-total.values {
		return fmt.Errorf("gent: expanded value exceeds %d values", values)
	}
	total.bytes += child.bytes
	total.values += child.values
	total.depth = max(total.depth, child.depth+1)
	return r.checkExpansion(*total)
}

func (r *CodecRegistry) measureLiteral(value savedJSONValue, depth int) (tableMeasure, error) {
	result := tableMeasure{bytes: len(value.bytes()), values: 1, depth: 1}
	if depth > maxTableDepth {
		return result, errors.New("gent: literal nesting exceeds 512")
	}
	visit := func(child savedJSONValue) error {
		measure, err := r.measureLiteral(child, depth+1)
		if err != nil {
			return err
		}
		// The literal bytes already include its children. Only count values and depth again.
		measure.bytes = 0
		return r.addExpansion(&result, measure)
	}
	switch value.kind() {
	case '{':
		if err := value.object(func(_ string, child savedJSONValue) error {
			return visit(child)
		}); err != nil {
			return result, err
		}
	case '[':
		if err := value.array(visit); err != nil {
			return result, err
		}
	}
	return result, r.checkExpansion(result)
}
