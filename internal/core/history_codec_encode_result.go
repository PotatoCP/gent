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
}

type savedValueEncoder interface {
	encodeValue(*valueEncoder, any, int) (json.RawMessage, error)
}

// Build nested envelopes as Go values and serialize them together. Serializing each container
// separately makes encoding/json scan and copy the same descendant JSON at every ancestor. The
// wire fields match EncodedValue; only scalar and custom-codec leaves contain JSON during encoding.
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
	result, err := encoder.encode(reflect.ValueOf(value), depth)
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
	data, err := json.Marshal(encoded)
	if err != nil {
		return nil, err
	}
	return data, encoder.checkEncodedSize(len(data))
}

func (encoder *valueEncoder) encodeCodec(
	codec ValueCodec, value any, depth int,
) (json.RawMessage, error) {
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
	return data, nil
}
