package gent

import (
	"encoding"
	"encoding/json"
	"fmt"
	"reflect"
	"unicode/utf8"
)

func validateStringValue(value reflect.Value) error {
	if value.Kind() != reflect.String || utf8.ValidString(value.String()) {
		return nil
	}
	// Explicit application methods own their wire meaning. In their absence JSON replaces bad
	// bytes with U+FFFD, which can lose content or collapse two map keys after an accepted save.
	typ := value.Type()
	if typ.Implements(reflect.TypeFor[json.Marshaler]()) ||
		typ.Implements(reflect.TypeFor[encoding.TextMarshaler]()) {
		return nil
	}
	return fmt.Errorf("gent: invalid UTF-8 in string value of type %s", typ)
}

// Compact framework wire structs bypass the ordinary scalar encoder. Validate their own text
// before JSON conversion too. Application codec payloads remain under their explicit contract.
func validateWireText(value, field string) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("gent: invalid UTF-8 in %s", field)
	}
	return nil
}

func validateEncodedValueText(value EncodedValue) error {
	if err := validateWireText(value.Type, "encoded value type"); err != nil {
		return err
	}
	return validateWireText(value.DeclaredType, "encoded value declared type")
}

// SaveError has already bounded and copied the error chain. Nil errors and nil causes are valid.
func validateSavedErrorText(value *SavedError) error {
	if value == nil {
		return nil
	}
	if err := validateWireText(value.Message, "saved error message"); err != nil {
		return err
	}
	for _, code := range value.Codes {
		if err := validateWireText(code, "saved error code"); err != nil {
			return err
		}
	}
	for _, cause := range value.Causes {
		if err := validateSavedErrorText(cause); err != nil {
			return err
		}
	}
	return nil
}

// Shared wire strings are immutable Go values. Decode a long shared literal once, while still
// constructing independent slices, maps, pointers, and byte buffers for every occurrence. Keys
// refer to this read's input only; the cache and input index are released when decoding ends.
func (decoder *valueDecoder) decodeString(value savedJSONValue) (string, error) {
	raw := value.bytes()
	cache := len(raw) >= 256 && value.table == nil
	if cache {
		if text, ok := decoder.strings[value]; ok {
			return text, nil
		}
	}
	var text string
	if err := decodeSavedJSONString(raw, &text); err != nil {
		return "", err
	}
	if cache {
		if decoder.strings == nil {
			decoder.strings = make(map[savedJSONValue]string)
		}
		decoder.strings[value] = text
	}
	return text, nil
}
