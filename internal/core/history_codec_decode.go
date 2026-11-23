package gent

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
)

func (r *valueDecoder) resolveType(name string) (reflect.Type, error) {
	return r.resolveTypeAtDepth(name, 0)
}

func (r *valueDecoder) resolveTypeAtDepth(name string, depth int) (reflect.Type, error) {
	if depth > maxCodecTypeDepth {
		return nil, fmt.Errorf("gent: type nesting exceeds %d", maxCodecTypeDepth)
	}
	switch name {
	case "any":
		return reflect.TypeFor[any](), nil
	case "content":
		return reflect.TypeFor[ContentPart](), nil
	case "error-interface":
		return reflect.TypeFor[error](), nil
	}
	if value, ok := r.lookupId(name); ok {
		return value.typ, r.checkValueAllocation(value.typ, 1)
	}
	if strings.HasPrefix(name, "*") {
		typ, err := r.resolveTypeAtDepth(name[1:], depth+1)
		if err != nil {
			return nil, err
		}
		return reflect.PointerTo(typ), nil
	}
	if strings.HasPrefix(name, "[]") {
		typ, err := r.resolveTypeAtDepth(name[2:], depth+1)
		if err != nil {
			return nil, err
		}
		return reflect.SliceOf(typ), nil
	}
	if strings.HasPrefix(name, "[") {
		end := strings.Index(name, "]")
		if end < 2 {
			return nil, errors.New("gent: invalid array type")
		}
		length, err := strconv.Atoi(name[1:end])
		if err != nil || length < 0 {
			return nil, errors.New("gent: invalid array size")
		}
		element, err := r.resolveTypeAtDepth(name[end+1:], depth+1)
		if err != nil {
			return nil, err
		}
		// Use the same byte-size bound as the writer. An independent length cap rejects valid
		// large byte arrays and zero-size arrays after their checkpoint state is accepted.
		if err := r.checkValueAllocation(element, length); err != nil {
			return nil, err
		}
		return reflect.ArrayOf(length, element), nil
	}
	if strings.HasPrefix(name, "map:") {
		var pair []string
		if err := json.Unmarshal([]byte(name[4:]), &pair); err != nil {
			return nil, err
		}
		if len(pair) != 2 {
			return nil, errors.New("gent: invalid map type")
		}
		key, err := r.resolveTypeAtDepth(pair[0], depth+1)
		if err != nil {
			return nil, err
		}
		if !key.Comparable() {
			return nil, errors.New("gent: map key is not comparable")
		}
		element, err := r.resolveTypeAtDepth(pair[1], depth+1)
		if err != nil {
			return nil, err
		}
		return reflect.MapOf(key, element), nil
	}
	r.status = valueDecodeIncompatible
	return nil, fmt.Errorf("gent: unknown codec %q", name)
}

func (r *valueDecoder) decode(value decodingValue, depth int) (reflect.Value, error) {
	return r.decodeExpected(value, depth, nil)
}

// A declared anonymous struct has no stable Go name. Use the current field type to decode its
// saved fields. DeclaredType is checked by the containing struct before this path is reached.
func (r *valueDecoder) decodeExpected(value decodingValue, depth int,
	expected reflect.Type,
) (reflect.Value, error) {
	if depth > maxCodecValueDepth {
		return reflect.Value{}, fmt.Errorf("gent: value nesting exceeds %d", maxCodecValueDepth)
	}
	if !value.Data.present() {
		return reflect.Value{}, errors.New("gent: invalid encoded JSON")
	}
	registration, registered := r.lookupId(value.Type)
	if expected != nil && expected.Kind() == reflect.Struct && expected.Name() == "" {
		registration, registered = r.lookupType(expected)
	}
	wantVersion := 1
	if registered {
		wantVersion = registration.version
	}
	if value.Version != wantVersion {
		r.status = valueDecodeIncompatible
		return reflect.Value{}, fmt.Errorf(
			"gent: unsupported %s version %d",
			value.Type,
			value.Version,
		)
	}
	switch value.Type {
	case "bytes":
		data, err := r.decodeBytes(value.Data.bytes())
		return reflect.ValueOf(data), err
	case "encoded-value":
		var saved EncodedValue
		err := json.Unmarshal(value.Data.bytes(), &saved)
		return reflect.ValueOf(saved), err
	case "raw-json-bytes":
		var data []byte
		if err := json.Unmarshal(value.Data.bytes(), &data); err != nil {
			return reflect.Value{}, err
		}
		if !json.Valid(data) {
			return reflect.Value{}, errors.New("gent: invalid raw JSON")
		}
		return reflect.ValueOf(json.RawMessage(data)), nil
	case "raw-json-nil":
		if string(value.Data.bytes()) != "null" {
			return reflect.Value{}, errors.New("gent: invalid nil JSON encoding")
		}
		return reflect.ValueOf(json.RawMessage(nil)), nil
	case "nil":
		if string(value.Data.bytes()) != "null" {
			return reflect.Value{}, errors.New("gent: invalid nil encoding")
		}
		return reflect.Value{}, nil
	case "time":
		var t time.Time
		err := json.Unmarshal(value.Data.bytes(), &t)
		return reflect.ValueOf(t), err
	case "error":
		var saved *SavedError
		if err := json.Unmarshal(value.Data.bytes(), &saved); err != nil {
			return reflect.Value{}, err
		}
		if saved == nil {
			return reflect.Value{}, nil
		}
		return reflect.ValueOf(saved), nil
	case "pointer", "slice", "array", "map":
		return r.decodeContainer(value, depth, expected)
	}
	if !registered {
		r.status = valueDecodeIncompatible
		return reflect.Value{}, fmt.Errorf("gent: unknown codec %q", value.Type)
	}
	if registration.codec != nil {
		v, err := r.decodeCodec(registration.codec, value.Data, depth)
		if err != nil {
			return reflect.Value{}, err
		}
		if reflect.TypeOf(v) != registration.typ {
			r.status = valueDecodeOperationFailed
			return reflect.Value{}, fmt.Errorf(
				"gent: codec %s decoded %T, expected %s",
				value.Type,
				v,
				registration.typ,
			)
		}
		return reflect.ValueOf(v), nil
	}
	if err := r.checkValueAllocation(registration.typ, 1); err != nil {
		return reflect.Value{}, err
	}
	switch registration.typ.Kind() {
	case reflect.Slice, reflect.Array, reflect.Map, reflect.Pointer:
		return r.decodeNamedContainer(value, registration.typ, depth)
	}
	result := reflect.New(registration.typ).Elem()
	// Named strings can define JSON or text decode methods. Only built-in strings can bypass
	// those methods and reuse a decoded literal without changing application behavior.
	if registration.typ == reflect.TypeFor[string]() {
		text, err := r.decodeString(value.Data)
		result.SetString(text)
		return result, err
	}
	if result.Kind() != reflect.Struct {
		err := json.Unmarshal(value.Data.bytes(), result.Addr().Interface())
		return result, err
	}
	fields, err := value.Data.fields()
	if err != nil {
		return reflect.Value{}, err
	}
	for name, field := range fields {
		target := result.FieldByName(name)
		if !target.IsValid() || !target.CanSet() {
			continue
		}
		if field.DeclaredType != r.declaredFieldType(target.Type()) {
			r.status = valueDecodeIncompatible
			return reflect.Value{}, fmt.Errorf("gent: changed declared type for %s.%s", value.Type, name)
		}
		decoded, err := r.decodeExpected(field, depth+1, target.Type())
		if err != nil {
			return reflect.Value{}, fmt.Errorf("%s.%s: %w", value.Type, name, err)
		}
		if err := setDecoded(target, decoded); err != nil {
			return reflect.Value{}, err
		}
	}
	return result, nil
}

func (r *valueDecoder) decodeNamedContainer(value decodingValue, typ reflect.Type, depth int) (
	reflect.Value, error,
) {
	container, err := value.Data.envelope()
	if err != nil {
		return reflect.Value{}, err
	}
	kind := typ.Kind().String()
	if typ.Kind() == reflect.Pointer {
		kind = "pointer"
	}
	if typ.Kind() == reflect.Slice && typ.Elem() == reflect.TypeFor[byte]() {
		kind = "bytes"
	}
	// The named envelope describes this container; only its children add value depth. Require
	// the writer's underlying kind before decoding at the same depth. Otherwise nested named
	// envelopes could recurse without a depth check. The normal decoder still checks versions.
	if container.Type != kind {
		return reflect.Value{}, fmt.Errorf("gent: named container %s requires %s encoding, got %q",
			value.Type, kind, container.Type)
	}
	decoded, err := r.decodeExpected(container, depth, typ)
	if err != nil {
		return reflect.Value{}, err
	}
	if !decoded.IsValid() || !decoded.Type().ConvertibleTo(typ) {
		return reflect.Value{}, fmt.Errorf("gent: invalid named container %s", value.Type)
	}
	return decoded.Convert(typ), nil
}

func setDecoded(target, value reflect.Value) error {
	if !value.IsValid() {
		switch target.Kind() {
		case reflect.Interface, reflect.Pointer, reflect.Slice, reflect.Map:
			target.SetZero()
			return nil
		default:
			return fmt.Errorf("gent: nil is not assignable to %s", target.Type())
		}
	}
	if !value.Type().AssignableTo(target.Type()) {
		return fmt.Errorf("gent: %s is not assignable to %s", value.Type(), target.Type())
	}
	target.Set(value)
	return nil
}

func (r *valueDecoder) decodeContainer(value decodingValue, depth int,
	expected reflect.Type,
) (reflect.Value, error) {
	c, err := value.Data.container()
	if err != nil {
		return reflect.Value{}, err
	}
	var element reflect.Type
	if expected != nil && (expected.Kind() == reflect.Pointer || expected.Kind() == reflect.Slice ||
		expected.Kind() == reflect.Array || expected.Kind() == reflect.Map) {
		element = expected.Elem()
	} else {
		var err error
		element, err = r.resolveType(c.Element)
		if err != nil {
			return reflect.Value{}, err
		}
	}
	if c.Nil && (len(c.Values) != 0 || len(c.Items) != 0) {
		return reflect.Value{}, errors.New("gent: nil container has content")
	}
	var result reflect.Value
	switch value.Type {
	case "pointer":
		if c.Nil {
			return reflect.Zero(reflect.PointerTo(element)), nil
		}
		if len(c.Values) != 1 {
			return reflect.Value{}, errors.New("gent: invalid pointer encoding")
		}
		result, err = r.newPointerValue(element)
		if err != nil {
			return reflect.Value{}, err
		}
		v, err := r.decodeExpected(c.Values[0], depth+1, element)
		if err != nil {
			return reflect.Value{}, err
		}
		return result, setDecoded(result.Elem(), v)
	case "map":
		var key reflect.Type
		if expected != nil && expected.Kind() == reflect.Map {
			key = expected.Key()
		} else {
			var err error
			key, err = r.resolveType(c.Key)
			if err != nil {
				return reflect.Value{}, err
			}
		}
		if !key.Comparable() {
			return reflect.Value{}, errors.New("gent: map key is not comparable")
		}
		typ := reflect.MapOf(key, element)
		if c.Nil {
			return reflect.Zero(typ), nil
		}
		if err := r.checkValueAllocation(key, len(c.Items)); err != nil {
			return reflect.Value{}, err
		}
		if err := r.checkValueAllocation(element, len(c.Items)); err != nil {
			return reflect.Value{}, err
		}
		result = reflect.MakeMapWithSize(typ, len(c.Items))
		if len(c.Items) == 0 {
			return result, nil
		}
		// SetMapIndex copies the assignment slots. Each decoded child still has independent
		// mutable storage, including equal saved pointer keys and repeated shared wire values.
		mapKey, mapValue := reflect.New(key).Elem(), reflect.New(element).Elem()
		for _, item := range c.Items {
			k, err := r.decodeExpected(item.Key, depth+1, key)
			if err != nil {
				return reflect.Value{}, err
			}
			v, err := r.decodeExpected(item.Value, depth+1, element)
			if err != nil {
				return reflect.Value{}, err
			}
			if err := setDecoded(mapKey, k); err != nil {
				return reflect.Value{}, err
			}
			if err := setDecoded(mapValue, v); err != nil {
				return reflect.Value{}, err
			}
			if !mapKey.Comparable() {
				return reflect.Value{}, errors.New("gent: unhashable map key")
			}
			if result.MapIndex(mapKey).IsValid() {
				return reflect.Value{}, errors.New("gent: duplicate map key")
			}
			result.SetMapIndex(mapKey, mapValue)
		}
		return result, nil
	case "array":
		if err := r.checkValueAllocation(element, len(c.Values)); err != nil {
			return reflect.Value{}, err
		}
		result = reflect.New(reflect.ArrayOf(len(c.Values), element)).Elem()
	default:
		if c.Nil {
			return reflect.Zero(reflect.SliceOf(element)), nil
		}
		if err := r.checkValueAllocation(element, len(c.Values)); err != nil {
			return reflect.Value{}, err
		}
		result = reflect.MakeSlice(reflect.SliceOf(element), len(c.Values), len(c.Values))
	}
	for i, item := range c.Values {
		v, err := r.decodeExpected(item, depth+1, element)
		if err != nil {
			return reflect.Value{}, err
		}
		if err := setDecoded(result.Index(i), v); err != nil {
			return reflect.Value{}, err
		}
	}
	return result, nil
}

// Named types retain identity while their exported fields can evolve. Container declarations
// remain exact. In particular, replacing string with any must not pass by assignability alone.
func (r *CodecRegistry) declaredFieldType(t reflect.Type) string {
	if registration, ok := r.lookupType(t); ok && t.Name() != "" {
		return registration.id
	}
	if t.Name() != "" {
		return valueTypeId(t)
	}
	switch t.Kind() {
	case reflect.Struct:
		return "struct"
	case reflect.Pointer:
		return "*" + r.declaredFieldType(t.Elem())
	case reflect.Slice:
		return "[]" + r.declaredFieldType(t.Elem())
	case reflect.Array:
		return fmt.Sprintf("[%d]%s", t.Len(), r.declaredFieldType(t.Elem()))
	case reflect.Map:
		return "map[" + r.declaredFieldType(t.Key()) + "]" + r.declaredFieldType(t.Elem())
	default:
		return t.String()
	}
}
