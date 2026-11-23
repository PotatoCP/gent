package gent

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sync"
	"time"
	"unicode/utf8"
)

// EncodedValue preserves concrete types across process boundaries. Ordinary JSON decoding of
// interface fields loses content-part implementations, numeric types, and typed tool metadata.
type EncodedValue struct {
	Type         string          `json:"type"`
	DeclaredType string          `json:"declaredType,omitempty"`
	Version      int             `json:"version"`
	Data         json.RawMessage `json:"data,omitempty"`
}

// ValueCodec supplies a versioned representation for an application-owned value. Decode must
// return an owned value and enforce its own allocation limits. Gent supplies a separate input
// buffer for each Decode call; do not return shared mutable results across calls.
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
	shape   *codecShape
}

// CodecRegistry binds wire names to concrete types. Register custom types before starting a run;
// decoding never loads code or guesses a Go type from an untrusted checkpoint.
type CodecRegistry struct {
	mu     sync.RWMutex
	byType map[reflect.Type]registeredValue
	byId   map[string]registeredValue
	limits CodecLimits
	// Built-in descriptions are immutable and shared. Application registrations and codecs
	// bound to this registry remain local, so one conversation cannot change another's types.
	builtin     *CodecRegistry
	bindings    []registeredValue
	compression CodecCompression
}

func NewCodecRegistry(options ...CodecOption) *CodecRegistry {
	builtin := builtinCodecRegistry()
	r := &CodecRegistry{byType: make(map[reflect.Type]registeredValue),
		byId: make(map[string]registeredValue), builtin: builtin,
		compression: CodecCompressionSnappy}
	for _, option := range options {
		option(r)
	}
	for _, value := range builtin.bindings {
		value.codec = value.codec.(registryBoundCodec).bind(r)
		r.add(value)
	}
	return r
}

type registryBoundCodec interface {
	bind(*CodecRegistry) ValueCodec
}

var builtinCodecRegistry = sync.OnceValue(newBuiltinCodecRegistry)

func newBuiltinCodecRegistry() *CodecRegistry {
	r := &CodecRegistry{
		byType: map[reflect.Type]registeredValue{},
		byId:   map[string]registeredValue{},
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
	for _, value := range r.byType {
		if value.codec != nil {
			r.bindings = append(r.bindings, value)
		}
	}
	return r
}

// Marshal preserves concrete content and metadata types in a self-contained JSON document.
// Document-local type, layout, and shared-value tables remove repeated descriptions and values.
// Large documents use lossless Snappy compression when it reduces their stored size. Use
// WithCodecCompression(CodecCompressionNone) to keep all tables directly readable as JSON.
// A version and digest cover the saved representation. Use this method for RunView and SaveBatch;
// plain JSON cannot restore interface-valued content without this type information.
// Ordinary strings must be valid UTF-8. A map must not depend on time location identity or
// monotonic readings to distinguish keys. Unsupported values fail before bytes are returned.
// Errors use *SavedError unless an explicit ValueCodec is bound to their concrete type. A field
// or container that cannot receive that representation fails before writing.
// Explicit application codecs and scalar marshal methods retain their own representation contract.
func (r *CodecRegistry) Marshal(value any) ([]byte, error) {
	encoder := newValueEncoder(r)
	return encoder.marshal(value)
}

// Unmarshal restores a Marshal result using the same versioned type registrations. It validates
// document integrity, references, and expansion limits before decoding typed values. Each use of
// a shared wire value creates an independent mutable Go value. A failed load leaves target intact.
// RunView validates its retained projection by saved logical values, not by pointer-key addresses.
func (r *CodecRegistry) Unmarshal(data []byte, target any) error {
	if err := r.checkEncodedSize(len(data)); err != nil {
		return err
	}
	destination := reflect.ValueOf(target)
	if destination.Kind() != reflect.Pointer || destination.IsNil() {
		return errors.New("gent: decode target must be a non-nil pointer")
	}
	decoder := valueDecoder{CodecRegistry: r, status: valueDecodeInvalid}
	decoded, err := decoder.document(data)
	if err != nil {
		return err
	}
	value := reflect.ValueOf(decoded)
	if !value.IsValid() {
		destination.Elem().SetZero()
		return nil
	}
	if value.Kind() == reflect.Pointer && !value.Type().AssignableTo(destination.Elem().Type()) {
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
	if value.codec == nil {
		value.shape = newCodecShape(value.typ)
	}
	r.byType[value.typ] = value
	r.byId[value.id] = value
}

// RegisterType registers a concrete type with the registry's recursive codecs. Structs use their
// exported fields.
// Errors still use the default *SavedError representation. Use Register with a ValueCodec when a
// concrete error field or container element cannot receive that representation.
// A caller-supplied version identifies semantic changes that Go reflection cannot detect.
// Names must contain valid UTF-8. Valid Unicode names, including U+FFFD, are supported.
// Names must not use reserved value tags or type aliases, or start with *, [, or map:.
// Reserved names are bytes, nil, pointer, slice, array, map, time, error, encoded-value,
// raw-json-bytes, raw-json-nil, any, content, and error-interface.
// An error leaves the registry intact.
func (r *CodecRegistry) RegisterType(id string, version int, sample any) error {
	return r.register(id, version, reflect.TypeOf(sample), nil)
}

// Register binds a custom codec to the sample's concrete type. Its Type must follow the name
// rules of RegisterType. An error leaves both the type and name bindings intact.
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
	// JSON replaces invalid UTF-8 bytes. Reject them before binding the original name, or a
	// successful save could lose its type identity or select another registered type on load.
	if !utf8.ValidString(id) {
		return fmt.Errorf("gent: codec name %q is not valid UTF-8", id)
	}
	if reservedCodecName(id) {
		return fmt.Errorf("gent: codec name %q is reserved", id)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if old, ok := r.registeredID(id); ok {
		return fmt.Errorf("gent: codec %q already registered for %s", id, old.typ)
	}
	if old, ok := r.registeredType(typ); ok {
		return fmt.Errorf("gent: type %s already registered as %q", typ, old.id)
	}
	r.add(registeredValue{id: id, version: version, typ: typ, codec: codec})
	return nil
}

func (r *CodecRegistry) lookupType(typ reflect.Type) (registeredValue, bool) {
	// Built-in reflective bindings cannot be replaced. Read them without the local registry
	// lock; sharing descriptions must not add lock contention to every primitive value.
	if r.builtin != nil {
		if value, ok := r.builtin.byType[typ]; ok && value.codec == nil {
			return value, true
		}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.registeredType(typ)
}

// registeredType and registeredID require the caller's lock. The fallback maps never change.
func (r *CodecRegistry) registeredType(typ reflect.Type) (registeredValue, bool) {
	value, ok := r.byType[typ]
	if !ok && r.builtin != nil {
		value, ok = r.builtin.byType[typ]
	}
	return value, ok
}

func (r *CodecRegistry) lookupId(id string) (registeredValue, bool) {
	if r.builtin != nil {
		if value, ok := r.builtin.byId[id]; ok && value.codec == nil {
			return value, true
		}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.registeredID(id)
}

func (r *CodecRegistry) registeredID(id string) (registeredValue, bool) {
	value, ok := r.byId[id]
	if !ok && r.builtin != nil {
		value, ok = r.builtin.byId[id]
	}
	return value, ok
}

// Encode returns a component's typed envelope with owned bytes. Use Encode/Decode for explicit
// EncodedValue fields and custom checkpoint components. Marshal/Unmarshal own the complete storage
// document and its tables; json.Marshal on an EncodedValue is not that document format.
// The same value rules as Marshal apply, before custom checkpoint data can be replaced.
// Cyclic values are rejected rather than retained as pointers into caller-owned state.
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

func (r *valueEncoder) encodeBytes(data []byte) (encodingValue, error) {
	// Repeated media buffers need one base64 conversion per document. The string key owns its
	// bytes, so this cache cannot depend on a caller changing a buffer after encoding returns.
	if len(data) >= 256 {
		if saved, ok := r.buffers[string(data)]; ok {
			return encodingValue{Type: "bytes", Version: 1, Data: saved}, nil
		}
	}
	value, err := encoded("bytes", data)
	if err != nil {
		return encodingValue{}, err
	}
	if len(data) >= 256 {
		if r.buffers == nil {
			r.buffers = make(map[string]json.RawMessage)
		}
		r.buffers[string(data)] = value.Data.(json.RawMessage)
	}
	return value, nil
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

// Value depth counts child fields, elements, keys, and pointer targets. A named type envelope
// or Go interface describes the same value, not another child. All value paths share this bound.
const maxCodecValueDepth = 100

func (r *valueEncoder) encode(v reflect.Value, depth int) (encodingValue, error) {
	return r.encodeExpected(v, depth, nil)
}

// A root component can return any decoded value. A field or container child must also fit its
// declared type. Carry that destination through interface unwrapping and check it where a built-in
// representation changes the concrete type. This avoids a second traversal or application Decode
// call on every write. Nil expected means there is no declared destination at this boundary.
func (r *valueEncoder) encodeExpected(
	v reflect.Value, depth int, expected reflect.Type,
) (encodingValue, error) {
	if depth > maxCodecValueDepth {
		return encodingValue{}, fmt.Errorf("gent: value nesting exceeds %d", maxCodecValueDepth)
	}
	if !v.IsValid() {
		return encoded("nil", nil)
	}
	if v.Kind() == reflect.Interface {
		if v.IsNil() {
			return encoded("nil", nil)
		}
		return r.encodeExpected(v.Elem(), depth, expected)
	}
	registration, registered := r.lookupType(v.Type())
	// Encoded payloads are already portable. Re-encoding each RawMessage byte through reflection
	// would multiply streamed event sizes and obscure the versioned payload they contain.
	if v.Type() == reflect.TypeFor[EncodedValue]() {
		value := v.Interface().(EncodedValue)
		if err := validateEncodedValueText(value); err != nil {
			return encodingValue{}, err
		}
		return encoded("encoded-value", value)
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
			value := SaveError(err)
			// An interface or root component loses a typed nil error's concrete type on load.
			// Use the same nil envelope before and after loading so batch receipts stay stable.
			// Concrete pointer destinations retain their declared type and existing encoding.
			if value == nil && (expected == nil || expected.Kind() == reflect.Interface) {
				return encoded("nil", nil)
			}
			savedType := reflect.TypeFor[*SavedError]()
			// A nil error decodes as the destination's zero value. It can still fit a concrete
			// pointer field, even when a non-nil SavedError could not fit that field.
			if value != nil && expected != nil && !savedType.AssignableTo(expected) {
				return encodingValue{}, fmt.Errorf(
					"gent: error value %s is saved as %s, which is not assignable to %s; "+
						"register a ValueCodec for the concrete error type", v.Type(), savedType, expected)
			}
			if err := validateSavedErrorText(value); err != nil {
				return encodingValue{}, err
			}
			return encoded("error", value)
		}
		if t, ok := v.Interface().(time.Time); ok {
			return encoded("time", t)
		}
	}
	if registered && registration.codec != nil {
		data, err := r.encodeCodec(registration.codec, v.Interface(), depth)
		if err != nil {
			return encodingValue{}, fmt.Errorf("encode %s: %w", registration.id, err)
		}
		return encodingValue{
			Type:    registration.id,
			Version: registration.version,
			Data:    data,
		}, nil
	}
	if err := r.checkValueAllocation(v.Type(), 1); err != nil {
		return encodingValue{}, err
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
		fields := make(map[string]encodingValue, registration.shape.valueCount)
		for i, field := range registration.shape.fields {
			if field.private {
				return encodingValue{}, fmt.Errorf(
					"gent: unexported field %s.%s",
					v.Type(),
					field.name,
				)
			}
			if field.callback {
				if !v.Field(i).IsNil() {
					return encodingValue{}, fmt.Errorf(
						"gent: callback %s is not serializable",
						field.name,
					)
				}
				continue
			}
			value, err := r.encodeExpected(v.Field(i), depth+1, field.typ)
			if err != nil {
				return encodingValue{}, fmt.Errorf("%s.%s: %w", v.Type(), field.name, err)
			}
			value.DeclaredType = r.declaredFieldType(field.typ)
			fields[field.name] = value
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
		// IDs, empty fields, and text recur within a document. Reuse their immutable JSON only
		// within this call. Bound the cache so a long stream of unique text has a fixed overhead.
		// Named values and application codecs keep their own encoding behavior.
		if v.Type() == reflect.TypeFor[string]() {
			text := v.String()
			if data, ok := r.strings[text]; ok {
				return encodingValue{Type: registration.id, Version: registration.version, Data: data}, nil
			}
			if err := validateStringValue(v); err != nil {
				return encodingValue{}, err
			}
			value, err := encoded(registration.id, text)
			if err != nil {
				return encodingValue{}, err
			}
			if len(r.strings) < 256 {
				if r.strings == nil {
					r.strings = make(map[string]json.RawMessage)
				}
				r.strings[text] = value.Data.(json.RawMessage)
			}
			value.Version = registration.version
			return value, nil
		}
		if err := validateStringValue(v); err != nil {
			return encodingValue{}, err
		}
		value, err := encoded(registration.id, v.Interface())
		value.Version = registration.version
		return value, err
	default:
		return encodingValue{}, fmt.Errorf("gent: unsupported value %s", v.Type())
	}
}

func (r *CodecRegistry) typeName(t reflect.Type) (string, error) {
	return newValueEncoder(r).typeNameAtDepth(t, 0)
}

// Bound type descriptions separately from value nesting. A nil container still needs a type,
// and storing that same container inside another value must not make its type unreadable.
// Read and write use this bound so Encode cannot accept checkpoint data that Decode rejects.
const maxCodecTypeDepth = 100

func (r *valueEncoder) typeName(t reflect.Type) (string, error) {
	if name, ok := r.typeNames[t]; ok {
		return name, nil
	}
	// Cache only a complete, checked description. Recursive checks still start at the actual
	// type depth, so a cached child cannot let an oversized parent bypass the depth bound.
	name, err := r.typeNameAtDepth(t, 0)
	if err != nil {
		return "", err
	}
	if r.typeNames == nil {
		r.typeNames = make(map[reflect.Type]string)
	}
	r.typeNames[t] = name
	return name, nil
}

func (r *valueEncoder) typeNameAtDepth(t reflect.Type, depth int) (string, error) {
	if depth > maxCodecTypeDepth {
		return "", fmt.Errorf("gent: type nesting exceeds %d", maxCodecTypeDepth)
	}
	if err := r.checkValueAllocation(t, 1); err != nil {
		return "", err
	}
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
		element, err := r.typeNameAtDepth(t.Elem(), depth+1)
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
		key, err := r.typeNameAtDepth(t.Key(), depth+1)
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
		return r.encodeBytes(v.Bytes())
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
			// The reader needs real storage even for a zero-size pointer target. Check the same
			// padded allocation here so a tight bound cannot accept an unreadable component.
			if err := r.checkValueAllocation(pointerStorageType(v.Type().Elem()), 1); err != nil {
				return encodingValue{}, err
			}
			item, err := r.encodeExpected(v.Elem(), depth+1, v.Type().Elem())
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
		if err := r.checkValueAllocation(v.Type().Key(), v.Len()); err != nil {
			return encodingValue{}, err
		}
		if err := r.checkValueAllocation(v.Type().Elem(), v.Len()); err != nil {
			return encodingValue{}, err
		}
		keys := mapKeyNormalizer{registry: r.CodecRegistry}
		if v.Len() > 0 {
			c.Items = make([]encodedMapItem[encodingValue], 0, min(v.Len(), codecInitialCapacity))
		}
		iter := v.MapRange()
		for iter.Next() {
			key, err := r.valueExpected(iter.Key().Interface(), depth+1, v.Type().Key())
			if err != nil {
				return encodingValue{}, err
			}
			if r.normalizedMapKey(iter.Key()) {
				duplicate, err := keys.duplicate(iter.Key())
				if err != nil {
					return encodingValue{}, err
				}
				if duplicate {
					return encodingValue{}, keys.collisionError(v.Type())
				}
			}
			value, err := r.encodeExpected(iter.Value(), depth+1, v.Type().Elem())
			if err != nil {
				return encodingValue{}, err
			}
			c.Items = append(c.Items, encodedMapItem[encodingValue]{key, value})
		}
		// Map iteration order cannot change a batch digest between equivalent encodings.
		if err := sortEncodedItems(c.Items); err != nil {
			return encodingValue{}, err
		}
		return encodingValue{Type: "map", Version: 1, Data: c}, nil
	default:
		if err := r.checkValueAllocation(v.Type().Elem(), v.Len()); err != nil {
			return encodingValue{}, err
		}
		if v.Len() > 0 {
			c.Values = make([]encodingValue, 0, min(v.Len(), codecInitialCapacity))
		}
		for i := 0; i < v.Len(); i++ {
			item, err := r.encodeExpected(v.Index(i), depth+1, v.Type().Elem())
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
	return r.cloneThroughCodec(value, 0)
}

func (r *CodecRegistry) cloneThroughCodec(value any, depth int) (any, error) {
	// A codec-backed child keeps its position in the containing value. Restarting at zero
	// could let Clone accept a value that a later Encode rejects at the same depth limit.
	encoded, err := newValueEncoder(r).value(value, depth)
	if err != nil {
		return nil, err
	}
	decoder := valueDecoder{CodecRegistry: r, status: valueDecodeInvalid}
	return decoder.value(encoded, depth)
}
