package gent

import (
	"encoding/json"
	"errors"
	"reflect"
)

// Private framework fields need an explicit codec. The wire structs retain typed accepted
// outcomes while diagnostic EncodedValues remain opaque. Restore never calls live preparation.
type frameworkValueCodec[T any] struct {
	name     string
	registry *CodecRegistry
	encode   func(*valueEncoder, T, int) (encodingValue, error)
	decode   func(*valueDecoder, decodingValue, int) (T, error)
}

func (c frameworkValueCodec[T]) Type() string { return c.name }
func (frameworkValueCodec[T]) Version() int   { return 1 }
func (c frameworkValueCodec[T]) Encode(value any) (json.RawMessage, error) {
	data, err := c.encodeValue(newValueEncoder(c.registry), value, 0)
	if err != nil {
		return nil, err
	}
	return json.Marshal(data)
}

func (c frameworkValueCodec[T]) encodeValue(
	encoder *valueEncoder, value any, depth int,
) (any, error) {
	encoded, err := c.encode(encoder, value.(T), depth+1)
	if err != nil {
		return nil, err
	}
	return encoded, nil
}
func (c frameworkValueCodec[T]) Decode(data json.RawMessage) (any, error) {
	decoder := valueDecoder{CodecRegistry: c.registry, status: valueDecodeInvalid}
	indexed, err := indexSavedJSON(data)
	if err != nil {
		return nil, err
	}
	return c.decodeValue(&decoder, indexed, 0)
}

func (c frameworkValueCodec[T]) decodeValue(
	decoder *valueDecoder, data savedJSONValue, depth int,
) (any, error) {
	encoded, err := data.envelope()
	if err != nil {
		return nil, err
	}
	return c.decode(decoder, encoded, depth+1)
}

func addFrameworkValueCodec[T any](
	r *CodecRegistry,
	encode func(
		*valueEncoder,
		T,
		int,
	) (
		encodingValue,
		error,
	),
	decode func(*valueDecoder, decodingValue, int) (T, error),
) {
	typ := reflect.TypeFor[T]()
	c := frameworkValueCodec[T]{name: valueTypeId(typ), registry: r, encode: encode, decode: decode}
	r.add(registeredValue{id: c.Type(), version: c.Version(), typ: typ, codec: c})
}

func registerToolStateCodecs(r *CodecRegistry) {
	for _, value := range []any{modelToolResponseData{}, toolCallRecordData{},
		ToolExecutionResponse{}, ToolRecoveryResponse{}, ToolSearchTrace{},
		ToolStatus(""), ToolResponseKind(""),
		ToolRecoveryStatus(""), SteeringMessages{}, PTCIntegerMode("")} {
		typ := reflect.TypeOf(value)
		r.add(registeredValue{id: valueTypeId(typ), version: 1, typ: typ})
	}
	addFrameworkValueCodec(
		r,
		func(
			encoder *valueEncoder,
			value DataContent,
			depth int,
		) (
			encodingValue,
			error,
		) {
			return encoder.encode(reflect.ValueOf(value.value), depth)
		},
		func(decoder *valueDecoder, value decodingValue, depth int) (DataContent, error) {
			decoded, err := decoder.decodedValue(value, depth)
			if err != nil {
				return DataContent{}, err
			}
			// Check portable content without constructing any original output Go type.
			if _, err := json.Marshal(decoded); err != nil {
				return DataContent{}, err
			}
			return DataContent{value: decoded}, nil
		},
	)
	addFrameworkValueCodec(
		r,
		func(
			encoder *valueEncoder,
			value ModelToolCallResponse,
			depth int,
		) (
			encodingValue,
			error,
		) {
			return encoder.encode(reflect.ValueOf(value.data), depth)
		},
		func(decoder *valueDecoder, value decodingValue, depth int) (ModelToolCallResponse, error) {
			data, err := decodeSavedAs[modelToolResponseData](decoder, value, depth)
			if err != nil {
				return ModelToolCallResponse{}, err
			}
			if err := validateSavedModelToolResponse(data); err != nil {
				return ModelToolCallResponse{}, err
			}
			return ModelToolCallResponse{data: data}, nil
		},
	)
	addFrameworkValueCodec(
		r,
		func(
			encoder *valueEncoder,
			value ToolCallRecord,
			depth int,
		) (
			encodingValue,
			error,
		) {
			return encoder.encode(reflect.ValueOf(value.data), depth)
		},
		func(decoder *valueDecoder, value decodingValue, depth int) (ToolCallRecord, error) {
			data, err := decodeSavedAs[toolCallRecordData](decoder, value, depth)
			if err != nil {
				return ToolCallRecord{}, err
			}
			if err := validateRecordOutcome(&data); err != nil {
				return ToolCallRecord{}, err
			}
			if data.Response == nil {
				return ToolCallRecord{}, errors.New("gent: saved reply is absent")
			}
			kind, execution, recovery := recordStatuses(&data)
			if got := data.Response.data; got.Kind != kind || got.ExecutionStatus != execution ||
				got.RecoveryStatus != recovery {
				return ToolCallRecord{}, errors.New("gent: saved reply metadata differs from its outcome")
			}
			return ToolCallRecord{data: data}, nil
		},
	)
}

func validateSavedModelToolResponse(data modelToolResponseData) error {
	if data.ToolCallID == "" || data.Name == "" {
		return errors.New("gent: saved reply lacks identity")
	}
	execution := data.ExecutionStatus
	validExecution := execution == ToolExecuted || execution == ToolBlocked ||
		execution == ToolInvalidArguments
	valid := false
	switch data.Kind {
	case ToolResponseExecution:
		valid = validExecution && data.RecoveryStatus == ""
	case ToolResponseRecovery:
		valid = (data.RecoveryStatus == ToolRecoveryCompleted && validExecution) ||
			((data.RecoveryStatus == ToolRecoveryNotStarted ||
				data.RecoveryStatus == ToolRecoveryUnresolved) && execution == "")
	case ToolResponseCallError, ToolResponseRecoveryError:
		valid = execution == "" && data.RecoveryStatus == ""
	}
	if !valid {
		return errors.New("gent: invalid saved reply metadata")
	}
	return ValidateContentParts(data.Parts)
}
