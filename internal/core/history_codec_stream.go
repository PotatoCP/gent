package gent

import (
	"encoding/json"
	"fmt"
	"strings"
)

// A chunk has a small fixed shape. Content uses registry envelopes to retain concrete parts and
// media bytes. Execution identity belongs to its RecordedEvent, not a second mutable payload copy.
type streamChunkCodec struct{ registry *CodecRegistry }

type savedStreamChunk struct {
	Content          *EncodedValue `json:"content,omitempty"`
	SourceMessageIds []string      `json:"sourceMessageIds,omitempty"`
}

func (streamChunkCodec) Type() string { return "github.com/rickchristie/gent/StreamChunk" }
func (streamChunkCodec) Version() int { return 4 }

func (c streamChunkCodec) Encode(value any) (json.RawMessage, error) {
	return c.encodeValue(newValueEncoder(c.registry), value, 0)
}

func (c streamChunkCodec) encodeValue(
	encoder *valueEncoder, value any, depth int,
) (json.RawMessage, error) {
	chunk := value.(StreamChunk)
	wire := savedStreamChunk{SourceMessageIds: chunk.SourceMessageIds}
	if chunk.Content != nil {
		encoded, err := encoder.value(chunk.Content, depth+1)
		if err != nil {
			return nil, err
		}
		wire.Content = &encoded
	}
	return json.Marshal(wire)
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
