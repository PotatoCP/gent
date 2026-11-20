package gent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// savedJSON indexes a validated document. Repeated json.Unmarshal calls on nested RawMessage
// fields scan and copy every descendant again at each level. An index lets the typed decoder
// read each envelope without that repeated work. The standard library still validates syntax.
// These ranges borrow input only during decoding. Values retained by callers must own their data.
type savedJSON struct {
	data []byte
	ends map[int]int
}

type savedJSONValue struct {
	document *savedJSON
	start    int
	end      int
	table    *savedTableNode
}

func indexSavedJSON(data []byte) (savedJSONValue, error) {
	if !json.Valid(data) {
		return savedJSONValue{}, errors.New("gent: invalid encoded JSON")
	}
	document := &savedJSON{data: data, ends: make(map[int]int)}
	var stack []int
	for i := 0; i < len(data); i++ {
		switch data[i] {
		case '"':
			i = savedJSONStringEnd(data, i) - 1
		case '{', '[':
			stack = append(stack, i)
		case '}', ']':
			last := len(stack) - 1
			document.ends[stack[last]] = i + 1
			stack = stack[:last]
		}
	}
	return savedJSONValue{document: document, end: len(data)}, nil
}

// Syntax is already valid. Skip escaped bytes so a quoted bracket cannot enter the index.
func savedJSONStringEnd(data []byte, start int) int {
	for i := start + 1; ; i++ {
		switch data[i] {
		case '\\':
			i++
		case '"':
			return i + 1
		}
	}
}

func savedJSONSpace(data []byte, start int) int {
	for start < len(data) {
		switch data[start] {
		case ' ', '\t', '\n', '\r':
			start++
		default:
			return start
		}
	}
	return start
}

func (document *savedJSON) valueAt(start int) savedJSONValue {
	start = savedJSONSpace(document.data, start)
	end := start
	switch document.data[start] {
	case '{', '[':
		end = document.ends[start]
	case '"':
		end = savedJSONStringEnd(document.data, start)
	default:
		for end < len(document.data) && !strings.ContainsRune(",]} \t\n\r", rune(document.data[end])) {
			end++
		}
	}
	return savedJSONValue{document: document, start: start, end: end}
}

func (value savedJSONValue) bytes() []byte {
	if value.table != nil {
		return value.appendJSON(make([]byte, 0, value.table.measure.bytes))
	}
	if value.document == nil { // An absent data field is different from a JSON null value.
		return nil
	}
	return value.document.data[value.start:value.end]
}

func (value savedJSONValue) present() bool {
	return value.document != nil || value.table != nil
}

func (value savedJSONValue) kind() byte {
	if value.table != nil {
		if value.table.layout == nil {
			return '['
		}
		return '{'
	}
	return value.document.data[savedJSONSpace(value.document.data, value.start)]
}

// Envelope names and type IDs normally need no unescaping. Avoid a full JSON decoder allocation
// for each small string. Escapes, invalid UTF-8, null, and wrong kinds keep the standard behavior.
func decodeSavedJSONString(data []byte, target *string) error {
	if data[0] == '"' && bytes.IndexByte(data, '\\') < 0 && utf8.Valid(data) {
		*target = string(data[1 : len(data)-1])
		return nil
	}
	return json.Unmarshal(data, target)
}

func (value savedJSONValue) integer(target *int) error {
	data := value.bytes()
	if number, err := strconv.Atoi(string(data)); err == nil {
		*target = number
		return nil
	}
	return json.Unmarshal(data, target)
}

func (value savedJSONValue) boolean(target *bool) error {
	switch value.kind() {
	case 't':
		*target = true
		return nil
	case 'f':
		*target = false
		return nil
	}
	return json.Unmarshal(value.bytes(), target)
}

func (value savedJSONValue) object(visit func(string, savedJSONValue) error) error {
	if value.table != nil {
		return value.table.object(visit)
	}
	if value.kind() == 'n' {
		return nil
	}
	if value.kind() != '{' {
		return errors.New("gent: expected a saved JSON object")
	}
	data := value.document.data
	position := savedJSONSpace(data, value.start) + 1
	for position = savedJSONSpace(data, position); data[position] != '}'; {
		end := savedJSONStringEnd(data, position)
		var name string
		if err := decodeSavedJSONString(data[position:end], &name); err != nil {
			return err
		}
		// The validated colon follows the field name. valueAt skips the following whitespace.
		child := value.document.valueAt(savedJSONSpace(data, end) + 1)
		if err := visit(name, child); err != nil {
			return err
		}
		position = savedJSONSpace(data, child.end)
		if data[position] == ',' {
			position = savedJSONSpace(data, position+1)
		}
	}
	return nil
}

func (value savedJSONValue) array(visit func(savedJSONValue) error) error {
	if value.table != nil {
		if value.table.layout != nil {
			return errors.New("gent: expected a saved JSON array")
		}
		for _, child := range value.table.values {
			if err := visit(child); err != nil {
				return err
			}
		}
		return nil
	}
	if value.kind() == 'n' {
		return nil
	}
	if value.kind() != '[' {
		return errors.New("gent: expected a saved JSON array")
	}
	data := value.document.data
	position := savedJSONSpace(data, value.start) + 1
	for position = savedJSONSpace(data, position); data[position] != ']'; {
		child := value.document.valueAt(position)
		if err := visit(child); err != nil {
			return err
		}
		position = savedJSONSpace(data, child.end)
		if data[position] == ',' {
			position = savedJSONSpace(data, position+1)
		}
	}
	return nil
}

type decodingValue struct {
	Type         string
	DeclaredType string
	Version      int
	Data         savedJSONValue
}

func (value savedJSONValue) envelope() (decodingValue, error) {
	var result decodingValue
	err := value.envelopeInto(&result)
	return result, err
}

func (value savedJSONValue) envelopeInto(result *decodingValue) error {
	return value.object(func(name string, field savedJSONValue) error {
		switch {
		case strings.EqualFold(name, "type"):
			return decodeSavedJSONString(field.bytes(), &result.Type)
		case strings.EqualFold(name, "declaredType"):
			return decodeSavedJSONString(field.bytes(), &result.DeclaredType)
		case strings.EqualFold(name, "version"):
			return field.integer(&result.Version)
		case strings.EqualFold(name, "data"):
			result.Data = field
		}
		return nil
	})
}

type decodingMapItem struct{ Key, Value decodingValue }

type decodingContainer struct {
	Element, Key string
	Nil          bool
	Values       []decodingValue
	Items        []decodingMapItem
}

func (value savedJSONValue) container() (decodingContainer, error) {
	var result decodingContainer
	err := value.object(func(name string, field savedJSONValue) error {
		switch {
		case strings.EqualFold(name, "element"):
			return decodeSavedJSONString(field.bytes(), &result.Element)
		case strings.EqualFold(name, "key"):
			return decodeSavedJSONString(field.bytes(), &result.Key)
		case strings.EqualFold(name, "nil"):
			return field.boolean(&result.Nil)
		case strings.EqualFold(name, "values"):
			result.Values = nil
			return field.array(func(item savedJSONValue) error {
				decoded, err := item.envelope()
				result.Values = append(result.Values, decoded)
				return err
			})
		case strings.EqualFold(name, "items"):
			result.Items = nil
			return field.array(func(item savedJSONValue) error {
				decoded, err := item.mapItem()
				result.Items = append(result.Items, decoded)
				return err
			})
		}
		return nil
	})
	return result, err
}

func (value savedJSONValue) mapItem() (decodingMapItem, error) {
	var result decodingMapItem
	err := value.object(func(name string, field savedJSONValue) error {
		var target *decodingValue
		switch {
		case strings.EqualFold(name, "key"):
			target = &result.Key
		case strings.EqualFold(name, "value"):
			target = &result.Value
		default:
			return nil
		}
		return field.envelopeInto(target)
	})
	return result, err
}

func (value savedJSONValue) fields() (map[string]decodingValue, error) {
	fields := make(map[string]decodingValue)
	err := value.object(func(name string, field savedJSONValue) error {
		decoded, err := field.envelope()
		if err != nil {
			return fmt.Errorf("gent: saved field %s: %w", name, err)
		}
		fields[name] = decoded
		return nil
	})
	return fields, err
}
