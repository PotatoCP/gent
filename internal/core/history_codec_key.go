package gent

import (
	"fmt"
	"reflect"
	"time"
)

type mapKeyNormalizer struct {
	registry  *CodecRegistry
	times     map[string]time.Time
	keys      map[any]bool
	nilErrors bool
}

func (normalizer *mapKeyNormalizer) duplicate(value reflect.Value) (bool, error) {
	key, err := normalizer.normalize(value)
	if err != nil {
		return false, err
	}
	if normalizer.keys == nil {
		normalizer.keys = make(map[any]bool)
	}
	identity := key.Interface()
	if normalizer.keys[identity] {
		return true, nil
	}
	normalizer.keys[identity] = true
	return false, nil
}

func (normalizer *mapKeyNormalizer) normalize(value reflect.Value) (reflect.Value, error) {
	if value.Kind() == reflect.Interface {
		if value.IsNil() {
			return value, nil
		}
		child := value.Elem()
		if normalizer.registry.normalizedNilError(child) {
			normalizer.nilErrors = true
			return reflect.Zero(value.Type()), nil
		}
		child, err := normalizer.normalize(child)
		if err != nil {
			return reflect.Value{}, err
		}
		result := reflect.New(value.Type()).Elem()
		result.Set(child)
		return result, nil
	}
	registry := normalizer.registry
	if registration, ok := registry.lookupType(value.Type()); ok && registration.codec != nil {
		return value, nil
	}
	if value.Type().Implements(reflect.TypeFor[error]()) {
		return value, nil
	}
	if value.Type() == reflect.TypeFor[time.Time]() {
		data, err := value.Interface().(time.Time).MarshalJSON()
		if err != nil {
			return reflect.Value{}, err
		}
		// Reuse a local normalized time for identical saved timestamps. Location allocation in
		// time.UnmarshalJSON must not decide whether a map is supported. The map still compares
		// other fields with Go key equality: for example, +0 and -0 are equal despite their JSON.
		if saved, ok := normalizer.times[string(data)]; ok {
			return reflect.ValueOf(saved), nil
		}
		var saved time.Time
		if err := saved.UnmarshalJSON(data); err != nil {
			return reflect.Value{}, err
		}
		if normalizer.times == nil {
			normalizer.times = make(map[string]time.Time)
		}
		normalizer.times[string(data)] = saved
		return reflect.ValueOf(saved), nil
	}
	if value.Kind() != reflect.Struct && value.Kind() != reflect.Array {
		return value, nil
	}
	result := reflect.New(value.Type()).Elem()
	count, source, target := value.NumField, value.Field, result.Field
	if value.Kind() == reflect.Array {
		count, source, target = value.Len, value.Index, result.Index
	}
	for i := 0; i < count(); i++ {
		child, err := normalizer.normalize(source(i))
		if err != nil {
			return reflect.Value{}, err
		}
		target(i).Set(child)
	}
	return result, nil
}

// Check both built-in conversions in the same comparison value. Separate checks can miss a key
// that becomes equal only after both a timestamp and a typed nil error have been normalized.
// JSON time values retain the timestamp and offset, not location identity or monotonic readings.
// Equal saved value keys cannot rely on those omitted distinctions. A non-nil framework pointer
// in a key is different: each decoded occurrence owns a distinct target, including zero-size ones.
// Application codecs keep their own normalization contract and are not called to inspect keys.
func (r *CodecRegistry) normalizedMapKey(value reflect.Value) bool {
	changed, hasPointer := r.mapKeyParts(value)
	return changed && !hasPointer
}

func (r *CodecRegistry) mapKeyParts(value reflect.Value) (bool, bool) {
	// Ordinary scalar keys need no built-in key conversion. Avoid a registry lookup for each key.
	switch value.Kind() {
	case reflect.Interface, reflect.Struct, reflect.Array, reflect.Pointer:
	default:
		if value.Type().NumMethod() == 0 {
			return false, false
		}
	}
	if value.Kind() == reflect.Interface {
		if value.IsNil() {
			// Include unchanged nil interfaces: another key can become nil after error conversion.
			return true, false
		}
		return r.mapKeyParts(value.Elem())
	}
	if registration, ok := r.lookupType(value.Type()); ok && registration.codec != nil {
		return false, false
	}
	if value.Type().Implements(reflect.TypeFor[error]()) {
		// Framework errors decode to independently owned SavedError pointers. Their private
		// fields are not reflective key content, and a non-nil error keeps entries distinct.
		isNil := value.Kind() == reflect.Pointer && value.IsNil()
		return isNil, !isNil
	}
	if value.Type() == reflect.TypeFor[time.Time]() {
		return true, false
	}
	if value.Kind() == reflect.Pointer {
		return false, !value.IsNil()
	}
	var count int
	var child func(int) reflect.Value
	switch value.Kind() {
	case reflect.Struct:
		count, child = value.NumField(), value.Field
	case reflect.Array:
		count, child = value.Len(), value.Index
	default:
		return false, false
	}
	changed := false
	for i := 0; i < count; i++ {
		found, pointer := r.mapKeyParts(child(i))
		if pointer {
			return false, true
		}
		changed = changed || found
	}
	return changed, false
}

// Explicit codecs keep the pointer type they register, including typed nil pointers. Default
// error encoding instead turns a typed nil error in an interface into that interface's nil value.
func (r *CodecRegistry) normalizedNilError(value reflect.Value) bool {
	if value.Kind() != reflect.Pointer || !value.IsNil() ||
		!value.Type().Implements(reflect.TypeFor[error]()) {
		return false
	}
	registration, registered := r.lookupType(value.Type())
	return !registered || registration.codec == nil
}

func (normalizer *mapKeyNormalizer) collisionError(typ reflect.Type) error {
	conversion := "time"
	if normalizer.nilErrors {
		conversion = "time or nil-error"
	}
	return fmt.Errorf("gent: map %s has keys that collide after %s normalization", typ, conversion)
}
