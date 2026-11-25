package gent

import (
	"encoding/json"
	"errors"
	"fmt"
)

type savedTableField struct {
	name     string
	nameJSON []byte
	constant savedJSONValue
}

type savedTableLayout struct {
	fields  []savedTableField
	slots   int
	measure tableMeasure
}

// This is a read-only view over the tables, not an expanded JSON buffer. The existing typed
// decoder visits these fields directly and allocates owned Go values for each occurrence.
type savedTableNode struct {
	layout  *savedTableLayout
	values  []savedJSONValue
	measure tableMeasure
}

type tableReadValue struct {
	value   savedJSONValue
	measure tableMeasure
}

type tableReader struct {
	registry *CodecRegistry
	types    []savedJSONValue
	layouts  []savedTableLayout
	shared   []tableReadValue
}

// V3 tables belong to one document. Loading never needs another document or process dictionary.
func (decoder *valueDecoder) documentV3(indexed savedJSONValue) (any, error) {
	fields, err := tableObject(indexed, "formatVersion", "data", "digest")
	if err != nil {
		return nil, err
	}
	var digest string
	if err := tableString(fields["digest"], &digest); err != nil {
		return nil, err
	}
	if digest != v3DocumentDigest(fields["data"].bytes()) {
		return nil, errors.New("gent: saved document digest mismatch")
	}
	dataValue, err := decoder.v3DocumentData(fields["data"])
	if err != nil {
		return nil, err
	}
	reader := tableReader{registry: decoder.CodecRegistry}
	root, err := reader.read(dataValue)
	if err != nil {
		return nil, err
	}
	value, err := root.envelope()
	if err != nil {
		return nil, err
	}
	// The physical document and its logical expansion have both passed their bounds. A
	// compressed literal can be larger than MaxEncodedBytes without exceeding either bound.
	decoder.documentRead = true
	return decoder.decodedValue(value, 0)
}

func tableObject(value savedJSONValue, names ...string) (map[string]savedJSONValue, error) {
	if !value.present() || value.kind() != '{' {
		return nil, errors.New("gent: expected a saved document object")
	}
	fields := make(map[string]savedJSONValue, len(names))
	allowed := make(map[string]bool, len(names))
	for _, name := range names {
		allowed[name] = true
	}
	err := value.object(func(name string, field savedJSONValue) error {
		if !allowed[name] {
			return fmt.Errorf("gent: unknown saved document field %q", name)
		}
		if _, exists := fields[name]; exists {
			return fmt.Errorf("gent: duplicate saved document field %q", name)
		}
		fields[name] = field
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		if _, exists := fields[name]; !exists {
			return nil, fmt.Errorf("gent: missing saved document field %q", name)
		}
	}
	return fields, nil
}

func tableInteger(value savedJSONValue) (int, error) {
	if !value.present() || (value.kind() != '-' && (value.kind() < '0' || value.kind() > '9')) {
		return 0, errors.New("gent: expected a saved document integer")
	}
	var result int
	err := value.integer(&result)
	return result, err
}

func tableString(value savedJSONValue, result *string) error {
	if !value.present() || value.kind() != '"' {
		return errors.New("gent: expected a saved document string")
	}
	return decodeSavedJSONString(value.bytes(), result)
}

func tableItems(value savedJSONValue) ([]savedJSONValue, error) {
	if value.kind() != '[' {
		return nil, errors.New("gent: expected a saved document array")
	}
	items := []savedJSONValue{}
	err := value.array(func(item savedJSONValue) error { items = append(items, item); return nil })
	return items, err
}

func (reader *tableReader) read(data savedJSONValue) (savedJSONValue, error) {
	fields, err := tableObject(data, "types", "layouts", "shared", "root")
	if err != nil {
		return savedJSONValue{}, err
	}
	types, err := tableItems(fields["types"])
	if err != nil {
		return savedJSONValue{}, err
	}
	seen := map[string]bool{}
	for _, value := range types {
		var name string
		if err := tableString(value, &name); err != nil {
			return savedJSONValue{}, err
		}
		if name == "" || seen[name] {
			return savedJSONValue{}, errors.New("gent: invalid type table")
		}
		seen[name] = true
		reader.types = append(reader.types, value)
	}
	layouts, err := tableItems(fields["layouts"])
	if err != nil {
		return savedJSONValue{}, err
	}
	for _, value := range layouts {
		layout, err := reader.layout(value)
		if err != nil {
			return savedJSONValue{}, err
		}
		reader.layouts = append(reader.layouts, layout)
	}
	shared, err := tableItems(fields["shared"])
	if err != nil {
		return savedJSONValue{}, err
	}
	for _, value := range shared {
		// Only previous entries are available. Missing, forward, and cyclic references fail
		// before a custom codec runs or a decoded Go object is constructed.
		decoded, err := reader.value(value, 0)
		if err != nil {
			return savedJSONValue{}, err
		}
		reader.shared = append(reader.shared, decoded)
	}
	root, err := reader.value(fields["root"], 0)
	return root.value, err
}

func (reader *tableReader) layout(value savedJSONValue) (savedTableLayout, error) {
	layout := savedTableLayout{measure: tableMeasure{bytes: 2, values: 1, depth: 1}}
	fields, err := tableItems(value)
	if err != nil {
		return layout, err
	}
	seen := map[string]bool{}
	for i, value := range fields {
		items, err := tableItems(value)
		if err != nil {
			return layout, err
		}
		if len(items) < 1 || len(items) > 2 {
			return layout, errors.New("gent: invalid layout field")
		}
		field := savedTableField{}
		if err := tableString(items[0], &field.name); err != nil {
			return layout, err
		}
		if seen[field.name] {
			return layout, errors.New("gent: duplicate layout field")
		}
		seen[field.name] = true
		field.nameJSON, err = json.Marshal(field.name)
		if err != nil {
			return layout, err
		}
		layout.measure.bytes += len(field.nameJSON) + 1
		if i > 0 {
			layout.measure.bytes++
		}
		if len(items) == 1 {
			layout.slots++
		} else {
			field.constant, err = reader.constant(items[1])
			if err != nil {
				return layout, err
			}
			if err := reader.registry.addExpansion(&layout.measure, tableMeasure{
				bytes: len(field.constant.bytes()), values: 1, depth: 1,
			}); err != nil {
				return layout, err
			}
		}
		layout.fields = append(layout.fields, field)
	}
	return layout, reader.registry.checkExpansion(layout.measure)
}

func (reader *tableReader) constant(value savedJSONValue) (savedJSONValue, error) {
	if value.kind() != '{' {
		return savedJSONValue{}, errors.New("gent: invalid layout constant")
	}
	var result savedJSONValue
	count := 0
	err := value.object(func(name string, value savedJSONValue) error {
		count++
		switch name {
		case "type":
			id, err := tableInteger(value)
			if err != nil {
				return err
			}
			if id < 0 || id >= len(reader.types) {
				return errors.New("gent: invalid type reference")
			}
			result = reader.types[id]
		case "constant":
			if value.kind() == '{' || value.kind() == '[' {
				return errors.New("gent: layout constants must be scalar")
			}
			result = value
		default:
			return fmt.Errorf("gent: unknown layout constant %q", name)
		}
		return nil
	})
	if err != nil {
		return savedJSONValue{}, err
	}
	if count != 1 {
		return savedJSONValue{}, errors.New("gent: invalid layout constant")
	}
	return result, nil
}

func (reader *tableReader) literal(value savedJSONValue) (tableReadValue, error) {
	measure, err := reader.registry.measureLiteral(value, 0)
	return tableReadValue{value: value, measure: measure}, err
}

func (reader *tableReader) value(value savedJSONValue, depth int) (tableReadValue, error) {
	if depth > maxTableDepth {
		return tableReadValue{}, errors.New("gent: document nesting exceeds 512")
	}
	if value.kind() == '{' {
		return tableReadValue{}, errors.New("gent: untagged document object")
	}
	if value.kind() != '[' {
		return reader.literal(value)
	}
	tag, argument, count, err := tableTag(value)
	if err != nil {
		return tableReadValue{}, err
	}
	kind, err := tableKind(tag)
	if err != nil {
		return tableReadValue{}, err
	}
	if kind == tableReference {
		if count != 2 {
			return tableReadValue{}, errors.New("gent: invalid shared reference")
		}
		id, err := tableInteger(argument)
		if err != nil {
			return tableReadValue{}, err
		}
		if id < 0 || id >= len(reader.shared) {
			return tableReadValue{}, errors.New("gent: invalid or forward shared reference")
		}
		return reader.shared[id], nil
	}
	if kind == tableLiteral {
		if count != 2 {
			return tableReadValue{}, errors.New("gent: invalid literal value")
		}
		return reader.literal(argument)
	}
	node := &savedTableNode{measure: tableMeasure{bytes: 2, values: 1, depth: 1}}
	if kind == tableRecord {
		if tag < 0 || tag >= len(reader.layouts) {
			return tableReadValue{}, errors.New("gent: invalid layout reference")
		}
		node.layout = &reader.layouts[tag]
		if count-1 != node.layout.slots {
			return tableReadValue{}, errors.New("gent: layout value count differs from its slots")
		}
		node.measure = node.layout.measure
	}
	node.values = make([]savedJSONValue, count-1)
	index := -1
	err = value.array(func(item savedJSONValue) error {
		index++
		if index == 0 {
			return nil
		}
		child, err := reader.value(item, depth+1)
		if err != nil {
			return err
		}
		if node.layout == nil && index > 1 {
			node.measure.bytes++
		}
		if err := reader.registry.addExpansion(&node.measure, child.measure); err != nil {
			return err
		}
		node.values[index-1] = child.value
		return nil
	})
	if err != nil {
		return tableReadValue{}, err
	}
	if err := reader.registry.checkExpansion(node.measure); err != nil {
		return tableReadValue{}, err
	}
	return tableReadValue{value: savedJSONValue{table: node}, measure: node.measure}, nil
}

// Count the immediate slots without allocating a temporary slice at every envelope. Child
// ranges are already indexed, so this does not scan or expand their descendants.
func tableTag(value savedJSONValue) (int, savedJSONValue, int, error) {
	tag, count := 0, 0
	var argument savedJSONValue
	err := value.array(func(item savedJSONValue) error {
		count++
		if count == 1 {
			var err error
			tag, err = tableInteger(item)
			return err
		}
		if count == 2 {
			argument = item
		}
		return nil
	})
	if err != nil {
		return 0, argument, count, err
	}
	if count == 0 {
		return 0, argument, 0, errors.New("gent: missing document value tag")
	}
	return tag, argument, count, nil
}

func (node *savedTableNode) object(visit func(string, savedJSONValue) error) error {
	if node.layout == nil {
		return errors.New("gent: expected a saved JSON object")
	}
	slot := 0
	for _, field := range node.layout.fields {
		value := field.constant
		if !value.present() {
			value = node.values[slot]
			slot++
		}
		if err := visit(field.name, value); err != nil {
			return err
		}
	}
	return nil
}

func (value savedJSONValue) appendJSON(data []byte) []byte {
	if value.table == nil {
		return append(data, value.bytes()...)
	}
	node := value.table
	if node.layout == nil {
		data = append(data, '[')
		for i, child := range node.values {
			if i > 0 {
				data = append(data, ',')
			}
			data = child.appendJSON(data)
		}
		return append(data, ']')
	}
	data = append(data, '{')
	slot := 0
	for i, field := range node.layout.fields {
		if i > 0 {
			data = append(data, ',')
		}
		data = append(data, field.nameJSON...)
		data = append(data, ':')
		child := field.constant
		if !child.present() {
			child = node.values[slot]
			slot++
		}
		data = child.appendJSON(data)
	}
	return append(data, '}')
}
