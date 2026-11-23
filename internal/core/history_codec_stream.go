package gent

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// A chunk has a small fixed shape. Content uses registry envelopes to retain concrete parts and
// media bytes. Execution identity belongs to its RecordedEvent, not a second mutable payload copy.
type streamChunkCodec struct{ registry *CodecRegistry }

func (c streamChunkCodec) bind(registry *CodecRegistry) ValueCodec {
	c.registry = registry
	return c
}

type savedStreamChunk struct {
	Content *encodingValue `json:"content,omitempty"`
	// Omit nil only. An empty non-nil slice is a distinct saved value.
	SourceMessageIds []string `json:"sourceMessageIds,omitzero"`
}

func (streamChunkCodec) Type() string { return "github.com/rickchristie/gent/StreamChunk" }
func (streamChunkCodec) Version() int { return 4 }

func (c streamChunkCodec) Encode(value any) (json.RawMessage, error) {
	data, err := c.encodeValue(newValueEncoder(c.registry), value, 0)
	if err != nil {
		return nil, err
	}
	return json.Marshal(data)
}

func (c streamChunkCodec) encodeValue(
	encoder *valueEncoder, value any, depth int,
) (any, error) {
	chunk := value.(StreamChunk)
	for _, id := range chunk.SourceMessageIds {
		if err := validateWireText(id, "stream source message ID"); err != nil {
			return nil, err
		}
	}
	wire := savedStreamChunk{SourceMessageIds: chunk.SourceMessageIds}
	if chunk.Content != nil {
		encoded, err := encoder.encode(reflect.ValueOf(chunk.Content), depth+1)
		if err != nil {
			return nil, err
		}
		wire.Content = &encoded
	}
	return wire, nil
}

func (c streamChunkCodec) Decode(data json.RawMessage) (any, error) {
	decoder := valueDecoder{CodecRegistry: c.registry, status: valueDecodeInvalid}
	indexed, err := indexSavedJSON(data)
	if err != nil {
		return nil, err
	}
	return c.decodeValue(&decoder, indexed, 0)
}

func (c streamChunkCodec) decodeValue(
	decoder *valueDecoder, data savedJSONValue, depth int,
) (any, error) {
	var content *decodingValue
	chunk := StreamChunk{}
	err := data.object(func(name string, field savedJSONValue) error {
		switch {
		case strings.EqualFold(name, "content"):
			if field.kind() == 'n' {
				content = nil
				return nil
			}
			if content == nil {
				content = &decodingValue{}
			}
			return field.envelopeInto(content)
		case strings.EqualFold(name, "sourceMessageIds"):
			return json.Unmarshal(field.bytes(), &chunk.SourceMessageIds)
		default:
			return fmt.Errorf("json: unknown field %q", name)
		}
	})
	if err != nil {
		return nil, err
	}
	if content != nil {
		parts, err := decodeSavedAs[[]ContentPart](decoder, *content, depth+1)
		if err != nil {
			return nil, fmt.Errorf("gent: saved stream content: %w", err)
		}
		chunk.Content = parts
	}
	return chunk, nil
}
