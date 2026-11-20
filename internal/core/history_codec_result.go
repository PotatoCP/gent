package gent

import (
	"encoding/json"
	"fmt"
)

// A decoder belongs to one read. Its status distinguishes saved-data conditions from a custom
// operation failure without matching error messages or storing mutable state on the registry.
type valueDecodeStatus string

const (
	// valueDecodeInvalid means a known saved representation violates its structural contract.
	valueDecodeInvalid valueDecodeStatus = "invalid"

	// valueDecodeIncompatible means a required codec, version, or declared type is unavailable.
	valueDecodeIncompatible valueDecodeStatus = "incompatible"

	// valueDecodeOperationFailed means application codec code failed to produce a valid value.
	valueDecodeOperationFailed valueDecodeStatus = "operation_failed"
)

type valueDecoder struct {
	*CodecRegistry
	status valueDecodeStatus
}

// Framework codecs keep nested validation on the same decoder. Application codecs retain their
// operation-error contract, including their original error and its errors.Is/errors.As behavior.
type savedValueCodec interface {
	decodeValue(*valueDecoder, savedJSONValue, int) (any, error)
}

func (decoder *valueDecoder) value(value EncodedValue, depth int) (any, error) {
	if err := decoder.checkEncodedSize(len(value.Data)); err != nil {
		return nil, err
	}
	data, err := indexSavedJSON(value.Data)
	if err != nil {
		return nil, err
	}
	return decoder.decodedValue(decodingValue{Type: value.Type, DeclaredType: value.DeclaredType,
		Version: value.Version, Data: data}, depth)
}

func (decoder *valueDecoder) decodedValue(value decodingValue, depth int) (any, error) {
	if value.Data.table == nil {
		if err := decoder.checkEncodedSize(len(value.Data.bytes())); err != nil {
			return nil, err
		}
	}
	decoded, err := decoder.decode(value, depth)
	if err != nil {
		return nil, err
	}
	if !decoded.IsValid() {
		return nil, nil
	}
	return decoded.Interface(), nil
}

func (decoder *valueDecoder) decodeCodec(
	codec ValueCodec, data savedJSONValue, depth int,
) (any, error) {
	if framework, ok := codec.(savedValueCodec); ok {
		return framework.decodeValue(decoder, data, depth)
	}
	// Custom code can retain or change its input. Give it its own buffer for that contract.
	value, err := codec.Decode(append(json.RawMessage(nil), data.bytes()...))
	if err != nil {
		decoder.status = valueDecodeOperationFailed
		return nil, fmt.Errorf("gent: decode codec %q: %w", codec.Type(), err)
	}
	return value, nil
}

func decodeSavedAs[T any](decoder *valueDecoder, value decodingValue, depth int) (T, error) {
	var zero T
	decoded, err := decoder.decodedValue(value, depth)
	if err != nil {
		return zero, err
	}
	if decoded == nil {
		return zero, nil
	}
	result, ok := decoded.(T)
	if !ok {
		return zero, fmt.Errorf("gent: decoded %T, expected %T", decoded, zero)
	}
	return result, nil
}
