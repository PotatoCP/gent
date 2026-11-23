package gent

import (
	"encoding/json"
	"fmt"
	"reflect"
)

type valueEncodeStatus string

const (
	// valueEncodeInvalid means the value cannot form a valid saved representation.
	valueEncodeInvalid valueEncodeStatus = "invalid"

	// valueEncodeIncompatible means the current registry lacks a required type binding.
	valueEncodeIncompatible valueEncodeStatus = "incompatible"

	// valueEncodeOperationFailed means an application codec failed during encoding.
	valueEncodeOperationFailed valueEncodeStatus = "operation_failed"
)

// Encoding one batch has local status and cycle tracking. Framework codecs share them so a
// missing nested binding remains an explicit replay result. Registry use remains concurrent.
type valueEncoder struct {
	*CodecRegistry
	status   valueEncodeStatus
	visiting map[any]bool
	strings  map[string]json.RawMessage
	buffers  map[string]json.RawMessage
}

type savedValueEncoder interface {
	encodeValue(*valueEncoder, any, int) (any, error)
}

// Build nested envelopes as Go values and serialize them together. Serializing each container
// separately makes encoding/json scan and copy the same descendant JSON at every ancestor. The
// component fields match EncodedValue; only scalar and application-codec leaves contain JSON.
// Storage tables consume this tree directly. Framework codecs keep their children in the tree.
type encodingValue struct {
	Type         string `json:"type"`
	DeclaredType string `json:"declaredType,omitempty"`
	Version      int    `json:"version"`
	Data         any    `json:"data,omitempty"`
}

func newValueEncoder(codecs *CodecRegistry) *valueEncoder {
	return &valueEncoder{CodecRegistry: codecs, status: valueEncodeInvalid,
		visiting: make(map[any]bool)}
}

func (encoder *valueEncoder) value(value any, depth int) (EncodedValue, error) {
	return encoder.valueExpected(value, depth, nil)
}

func (encoder *valueEncoder) valueExpected(
	value any, depth int, expected reflect.Type,
) (EncodedValue, error) {
	result, err := encoder.encodeExpected(reflect.ValueOf(value), depth, expected)
	if err != nil {
		return EncodedValue{}, err
	}
	data, ready := result.Data.(json.RawMessage)
	if !ready {
		data, err = json.Marshal(result.Data)
		if err != nil {
			return EncodedValue{}, err
		}
	}
	return EncodedValue{Type: result.Type, DeclaredType: result.DeclaredType,
		Version: result.Version, Data: data}, encoder.checkEncodedSize(len(data))
}

func (encoder *valueEncoder) marshal(value any) ([]byte, error) {
	encoded, err := encoder.encode(reflect.ValueOf(value), 0)
	if err != nil {
		return nil, err
	}
	data, err := encoder.marshalDocument(encoded)
	if err != nil {
		return nil, err
	}
	return data, encoder.checkEncodedSize(len(data))
}

func (encoder *valueEncoder) encodeCodec(
	codec ValueCodec, value any, depth int,
) (any, error) {
	if framework, ok := codec.(savedValueEncoder); ok {
		return framework.encodeValue(encoder, value, depth)
	}
	data, err := codec.Encode(value)
	if err != nil {
		encoder.status = valueEncodeOperationFailed
		return nil, err
	}
	if !json.Valid(data) {
		encoder.status = valueEncodeOperationFailed
		return nil, fmt.Errorf("codec %s returned invalid JSON", codec.Type())
	}
	// Application codecs can reuse their buffer after this call. The saved value must own it.
	return append(json.RawMessage(nil), data...), nil
}
