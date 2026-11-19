package gent

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sync"
	"time"
)

// EncodedValue preserves concrete types across process boundaries. Ordinary JSON decoding of
// interface fields loses content-part implementations, numeric types, and typed tool metadata.
type EncodedValue struct {
	Type         string          `json:"type"`
	DeclaredType string          `json:"declaredType,omitempty"`
	Version      int             `json:"version"`
	Data         json.RawMessage `json:"data,omitempty"`
}

// ValueCodec supplies a versioned representation for an application-owned value.
type ValueCodec interface {
	Type() string
	Version() int
	Encode(any) (json.RawMessage, error)
	Decode(json.RawMessage) (any, error)
}

type registeredValue struct {
	id      string
	version int
	typ     reflect.Type
	codec   ValueCodec
}

// CodecRegistry binds wire names to concrete types. Register custom types before starting a run;
// decoding never loads code or guesses a Go type from an untrusted checkpoint.
type CodecRegistry struct {
	mu     sync.RWMutex
	byType map[reflect.Type]registeredValue
	byId   map[string]registeredValue
	limits CodecLimits
}

func NewCodecRegistry(options ...CodecOption) *CodecRegistry {
	r := &CodecRegistry{
		byType: map[reflect.Type]registeredValue{},
		byId:   map[string]registeredValue{},
	}
	for _, option := range options {
		option(r)
	}
	values := []any{false, "", int(0), int8(0), int16(0), int32(0), int64(0), uint(0), uint8(0),
		uint16(0), uint32(0), uint64(0), float32(0), float64(0), json.Number(""), time.Duration(0),
		ChatMessageType(""), RecordOrigin(""), RecordMetadataKey(""),
		MessageContentMetadataKey(
			"",
		), TextContent{}, ImageURLContent{}, BinaryContent{},
		ModelToolCallRequest{}, FunctionCall{}, CachedContent{},
		CacheControl{},
		ToolRecoveryResponse{}, ToolRecoveryStatus(""),
		HistoryEntry{}, MessageContent{},
		Task{},
		ProviderContinuation{},
		ModelCallResponse{},
		ContentChoice{},
		ModelCompletion{},
		ModelRefusal{},
		ModelStatus(""),
		GenerationInfo{}, CallOptions{}, AssistantContent{}, AssistantContentType(""), ContentSource{},
		ToolDefinition{}, FunctionDefinition{}, ToolChoice{}, FunctionReference{},
		ModelMessage{}, ThinkingConfig{}, ThinkingMode(""), json.RawMessage{},
		FormattedSection{}, time.Time{}}
	for _, value := range values {
		typ := reflect.TypeOf(value)
		id := valueTypeId(typ)
		r.add(registeredValue{id: id, version: 1, typ: typ})
	}
	registerFrameworkCodecs(r)
	values = []any{HistoryEntryKind(""), ContextChangeKind(""), ExecutionPhase(""),
		HistorySnapshot{}, HistoryChange{}, HistoryUpdate{}, CompactionRecord{}, ContextSelection{},
		StateValue{}, RecordedEvent{}, SaveBatch{}, RunView{}, runViewData{}, runStateData{},
		RuntimeState{}, ContextRuntime{}, historySource{},
		Checkpoint{}, ContextCheckpoint{}, SavedCheckpointState{},
		SavedOutcome{}, UserMessageRetentionEntry{}, UserMessageRetentionInput{},
		UserMessageRetentionResult{},
	}
	for _, value := range values {
		typ := reflect.TypeOf(value)
		r.add(registeredValue{id: valueTypeId(typ), version: 1, typ: typ})
	}
	registerToolStateCodecs(r)
	registerRunViewCodec(r)
	return r
}

// Marshal preserves concrete content and metadata types in portable JSON. Use it for saved runs
// and batches; plain JSON cannot restore interface-valued content without this type information.
func (r *CodecRegistry) Marshal(value any) ([]byte, error) {
	encoder := newValueEncoder(r)
	return encoder.marshal(value)
}

// Unmarshal restores a Marshal result using the same versioned type registrations.
func (r *CodecRegistry) Unmarshal(data []byte, target any) error {
	if err := r.checkEncodedSize(len(data)); err != nil {
		return err
	}
	destination := reflect.ValueOf(target)
	if destination.Kind() != reflect.Pointer || destination.IsNil() {
		return errors.New("gent: decode target must be a non-nil pointer")
	}
	var encoded EncodedValue
	if err := json.Unmarshal(data, &encoded); err != nil {
		return err
	}
	decoded, err := r.Decode(encoded)
	if err != nil {
		return err
	}
	value := reflect.ValueOf(decoded)
	if !value.IsValid() {
		destination.Elem().SetZero()
		return nil
	}
	if value.Kind() == reflect.Pointer && value.Type() != destination.Elem().Type() {
		if value.IsNil() {
			return errors.New("gent: cannot decode nil into a value")
		}
		value = value.Elem()
	}
	if !value.Type().AssignableTo(destination.Elem().Type()) {
		return fmt.Errorf("gent: decoded %s, expected %s", value.Type(), destination.Elem().Type())
	}
	destination.Elem().Set(value)
	return nil
}

func cloneOwned[T any](registry *CodecRegistry, value T) (T, error) {
	cloned, err := registry.Clone(value)
	if err != nil {
		var zero T
		return zero, err
	}
	if cloned == nil {
		var zero T
		return zero, nil
	}
	result, ok := cloned.(T)
	if !ok {
		var zero T
		return zero, fmt.Errorf("gent: cloned %T, expected %T", cloned, zero)
	}
	return result, nil
}

func (r *CodecRegistry) add(value registeredValue) {
	r.byType[value.typ] = value
	r.byId[value.id] = value
}

// RegisterType registers a struct using its exported fields and the registry's recursive codecs.
// A caller-supplied version identifies semantic changes that Go reflection cannot detect.
func (r *CodecRegistry) RegisterType(id string, version int, sample any) error {
	return r.register(id, version, reflect.TypeOf(sample), nil)
}

func (r *CodecRegistry) Register(sample any, codec ValueCodec) error {
	if codec == nil {
		return errors.New("gent: nil value codec")
	}
	return r.register(codec.Type(), codec.Version(), reflect.TypeOf(sample), codec)
}

func (r *CodecRegistry) register(id string, version int, typ reflect.Type, codec ValueCodec) error {
	if id == "" || version < 1 || typ == nil {
		return errors.New("gent: invalid codec registration")
	}
	if id == "bytes" {
		return errors.New("gent: codec name bytes is reserved")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if old, ok := r.byId[id]; ok {
		return fmt.Errorf("gent: codec %q already registered for %s", id, old.typ)
	}
	if old, ok := r.byType[typ]; ok {
		return fmt.Errorf("gent: type %s already registered as %q", typ, old.id)
	}
	r.add(registeredValue{id: id, version: version, typ: typ, codec: codec})
	return nil
}

func (r *CodecRegistry) lookupType(typ reflect.Type) (registeredValue, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	value, ok := r.byType[typ]
	return value, ok
}

func (r *CodecRegistry) lookupId(id string) (registeredValue, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	value, ok := r.byId[id]
	return value, ok
}

// Encode takes ownership by returning newly allocated bytes. Cyclic values are rejected rather
// than retained as pointers into caller-owned state.
func (r *CodecRegistry) Encode(value any) (EncodedValue, error) {
	encoder := newValueEncoder(r)
	return encoder.value(value, 0)
}

func encoded(id string, value any) (encodingValue, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return encodingValue{}, err
	}
	return encodingValue{Type: id, Version: 1, Data: json.RawMessage(data)}, nil
}

type encodedMapItem[T any] struct {
	Key   EncodedValue `json:"key"`
	Value T            `json:"value"`
}

type encodedContainer[T any] struct {
	Element string              `json:"element,omitempty"`
	Key     string              `json:"key,omitempty"`
	Nil     bool                `json:"nil,omitempty"`
	Values  []T                 `json:"values,omitempty"`
	Items   []encodedMapItem[T] `json:"items,omitempty"`
}

func (r *valueEncoder) encode(v reflect.Value, depth int) (encodingValue, error) {
	if depth > 100 {
		return encodingValue{}, errors.New("gent: value nesting exceeds 100")
	}
	if !v.IsValid() {
		return encoded("nil", nil)
	}
	if v.Kind() == reflect.Interface {
		if v.IsNil() {
			return encoded("nil", nil)
		}
		return r.encode(v.Elem(), depth+1)
	}
	registration, registered := r.lookupType(v.Type())
	// Encoded payloads are already portable. Re-encoding each RawMessage byte through reflection
	// would multiply streamed event sizes and obscure the versioned payload they contain.
	if v.Type() == reflect.TypeFor[EncodedValue]() {
		return encoded("encoded-value", v.Interface())
	}
	if v.Type() == reflect.TypeFor[json.RawMessage]() {
		if v.IsNil() {
			return encoded("raw-json-nil", nil)
		}
		if !json.Valid(v.Bytes()) {
			return encodingValue{}, errors.New("gent: invalid raw JSON")
		}
		// JSON marshaling rewrites RawMessage whitespace and escapes. Save bytes so opaque
		// provider items retain their exact representation through copies and saved runs.
		return encoded("raw-json-bytes", v.Bytes())
	}
	if v.CanInterface() && (!registered || registration.codec == nil) {
		if err, ok := v.Interface().(error); ok {
			return encoded("error", SaveError(err))
		}
		if t, ok := v.Interface().(time.Time); ok {
			return encoded("time", t)
		}
	}
	if registered && registration.codec != nil {
		data, err := r.encodeCodec(registration.codec, v.Interface(), depth+1)
		if err != nil {
			return encodingValue{}, fmt.Errorf("encode %s: %w", registration.id, err)
		}
		return encodingValue{
			Type:    registration.id,
			Version: registration.version,
			Data:    append(json.RawMessage(nil), data...),
		}, nil
	}
	if v.Kind() == reflect.Pointer || v.Kind() == reflect.Map || v.Kind() == reflect.Slice {
		if !v.IsNil() {
			key := struct {
				typ     reflect.Type
				pointer uintptr
			}{v.Type(), v.Pointer()}
			if r.visiting[key] {
				return encodingValue{}, fmt.Errorf("gent: cyclic value %s", v.Type())
			}
			r.visiting[key] = true
			defer delete(r.visiting, key)
		}
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Map:
		if v.Type().Name() != "" && !registered {
			r.status = valueEncodeIncompatible
			return encodingValue{}, fmt.Errorf("gent: no codec for named container %s", v.Type())
		}
		container, err := r.encodeContainer(v, depth)
		if err != nil || !registered {
			return container, err
		}
		return encodingValue{Type: registration.id, Version: registration.version,
			Data: container}, nil
	case reflect.Struct:
		if !registered {
			r.status = valueEncodeIncompatible
			return encodingValue{}, fmt.Errorf("gent: no codec for %s", v.Type())
		}
		fields := map[string]encodingValue{}
		for i := 0; i < v.NumField(); i++ {
			field := v.Type().Field(i)
			if field.PkgPath != "" {
				return encodingValue{}, fmt.Errorf(
					"gent: unexported field %s.%s",
					v.Type(),
					field.Name,
				)
			}
			if field.Type.Kind() == reflect.Func && field.Tag.Get("json") == "-" {
				if !v.Field(i).IsNil() {
					return encodingValue{}, fmt.Errorf(
						"gent: callback %s is not serializable",
						field.Name,
					)
				}
				continue
			}
			value, err := r.encode(v.Field(i), depth+1)
			if err != nil {
				return encodingValue{}, fmt.Errorf("%s.%s: %w", v.Type(), field.Name, err)
			}
			value.DeclaredType = r.declaredFieldType(field.Type)
			fields[field.Name] = value
		}
		return encodingValue{Type: registration.id, Version: registration.version, Data: fields}, nil
	case reflect.Float32, reflect.Float64:
		if math.IsInf(v.Float(), 0) || math.IsNaN(v.Float()) {
			return encodingValue{}, errors.New("gent: non-finite number")
		}
		fallthrough
	case reflect.Bool, reflect.String, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32,
		reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if !registered {
			r.status = valueEncodeIncompatible
			return encodingValue{}, fmt.Errorf("gent: no codec for %s", v.Type())
		}
		value, err := encoded(registration.id, v.Interface())
		value.Version = registration.version
		return value, err
	default:
		return encodingValue{}, fmt.Errorf("gent: unsupported value %s", v.Type())
	}
}

func (r *CodecRegistry) typeName(t reflect.Type) (string, error) {
	return newValueEncoder(r).typeName(t)
}

func (r *valueEncoder) typeName(t reflect.Type) (string, error) {
	if t.Kind() == reflect.Interface {
		if t.NumMethod() == 0 {
			return "any", nil
		}
		if t == reflect.TypeFor[ContentPart]() {
			return "content", nil
		}
		if t == reflect.TypeFor[error]() {
			return "error-interface", nil
		}
	}
	if value, ok := r.lookupType(t); ok {
		return value.id, nil
	}
	if t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Map ||
		t.Kind() == reflect.Array {
		element, err := r.typeName(t.Elem())
		if err != nil {
			return "", err
		}
		if t.Kind() == reflect.Pointer {
			return "*" + element, nil
		}
		if t.Kind() == reflect.Slice {
			return "[]" + element, nil
		}
		if t.Kind() == reflect.Array {
			return fmt.Sprintf("[%d]%s", t.Len(), element), nil
		}
		key, err := r.typeName(t.Key())
		if err != nil {
			return "", err
		}
		data, err := json.Marshal([]string{key, element})
		return "map:" + string(data), err
	}
	r.status = valueEncodeIncompatible
	return "", fmt.Errorf("gent: no codec for container element %s", t)
}

func (r *valueEncoder) encodeContainer(v reflect.Value, depth int) (encodingValue, error) {
	// A byte buffer is one value, not a sequence of independently typed numbers. Named slices
	// keep their outer type envelope; registered custom codecs have already taken precedence.
	if v.Kind() == reflect.Slice && v.Type().Elem() == reflect.TypeFor[byte]() {
		if err := r.checkBinarySize(v.Len()); err != nil {
			return encodingValue{}, err
		}
		return encoded("bytes", v.Bytes())
	}
	name, err := r.typeName(v.Type().Elem())
	if err != nil {
		return encodingValue{}, err
	}
	c := encodedContainer[encodingValue]{Element: name}
	if v.Kind() != reflect.Array {
		c.Nil = v.IsNil()
	}
	switch v.Kind() {
	case reflect.Pointer:
		if !c.Nil {
			item, err := r.encode(v.Elem(), depth+1)
			if err != nil {
				return encodingValue{}, err
			}
			c.Values = []encodingValue{item}
		}
		return encodingValue{Type: "pointer", Version: 1, Data: c}, nil
	case reflect.Map:
		c.Key, err = r.typeName(v.Type().Key())
		if err != nil {
			return encodingValue{}, err
		}
		iter := v.MapRange()
		for iter.Next() {
			key, err := r.value(iter.Key().Interface(), depth+1)
			if err != nil {
				return encodingValue{}, err
			}
			value, err := r.encode(iter.Value(), depth+1)
			if err != nil {
				return encodingValue{}, err
			}
			c.Items = append(c.Items, encodedMapItem[encodingValue]{key, value})
		}
		// Map iteration order cannot change a batch digest between equivalent encodings.
		sortEncodedItems(c.Items)
		return encodingValue{Type: "map", Version: 1, Data: c}, nil
	default:
		for i := 0; i < v.Len(); i++ {
			item, err := r.encode(v.Index(i), depth+1)
			if err != nil {
				return encodingValue{}, err
			}
			c.Values = append(c.Values, item)
		}
		if v.Kind() == reflect.Array {
			return encodingValue{Type: "array", Version: 1, Data: c}, nil
		}
		return encodingValue{Type: "slice", Version: 1, Data: c}, nil
	}
}

// Decode validates every type and version before constructing an owned Go value.
func (r *CodecRegistry) Decode(value EncodedValue) (any, error) {
	decoder := valueDecoder{CodecRegistry: r, status: valueDecodeInvalid}
	return decoder.value(value, 0)
}

func (r *CodecRegistry) Clone(value any) (any, error) {
	// Limits on the wire representation need the exact encoded size. Otherwise owned copies
	// can retain registered Go types directly, without building and parsing temporary JSON.
	if r.limits.MaxEncodedBytes == 0 {
		result, supported, err := r.cloneValue(reflect.ValueOf(value), make(map[any]bool), 0)
		if err != nil {
			return nil, err
		}
		if supported {
			if !result.IsValid() {
				return nil, nil
			}
			return result.Interface(), nil
		}
	}
	return r.cloneThroughCodec(value)
}

func (r *CodecRegistry) cloneThroughCodec(value any) (any, error) {
	encoded, err := r.Encode(value)
	if err != nil {
		return nil, err
	}
	return r.Decode(encoded)
}
