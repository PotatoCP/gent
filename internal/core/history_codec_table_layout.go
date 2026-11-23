package gent

import (
	"encoding/json"
	"strconv"
)

type tableFieldKind string

const (
	// tableVariableField reads the next value from the saved record.
	tableVariableField tableFieldKind = "variable"
	// tableTypeField reads a constant name from the document's type table.
	tableTypeField tableFieldKind = "type"
	// tableConstantField supplies a constant JSON value for this field.
	tableConstantField tableFieldKind = "constant"
)

type tableBuildField struct {
	name     string
	kind     tableFieldKind
	typeID   int
	constant string
}

type tableBuildLayout struct {
	fields  []tableBuildField
	wire    json.RawMessage
	measure tableMeasure
}

func tableField(name string) tableBuildField {
	return tableBuildField{name: name, kind: tableVariableField}
}

func (builder *tableBuilder) typeField(name, value string) tableBuildField {
	return tableBuildField{name: name, kind: tableTypeField, typeID: builder.typeID(value)}
}

// Constants come from the fixed grammar: a codec version or the JSON literal true. Their
// representation is already valid JSON. Application content never enters through this helper.
func tableConstant(name, value string) tableBuildField {
	return tableBuildField{name: name, kind: tableConstantField, constant: value}
}

func (builder *tableBuilder) buildLayout(fields []tableBuildField) (tableBuildLayout, error) {
	layout := tableBuildLayout{fields: fields, wire: []byte{'['},
		measure: tableMeasure{bytes: 2, values: 1, depth: 1}}
	for index, field := range fields {
		name, err := json.Marshal(field.name)
		if err != nil {
			return layout, err
		}
		if index > 0 {
			layout.wire = append(layout.wire, ',')
			layout.measure.bytes++
		}
		layout.wire = append(layout.wire, '[')
		layout.wire = append(layout.wire, name...)
		layout.measure.bytes += len(name) + 1
		var size int
		switch field.kind {
		case tableVariableField:
			layout.wire = append(layout.wire, ']')
			continue
		case tableTypeField:
			layout.wire = append(layout.wire, `,{"type":`...)
			layout.wire = strconv.AppendInt(layout.wire, int64(field.typeID), 10)
			value, err := json.Marshal(builder.types[field.typeID])
			if err != nil {
				return layout, err
			}
			size = len(value)
		case tableConstantField:
			layout.wire = append(layout.wire, `,{"constant":`...)
			layout.wire = append(layout.wire, field.constant...)
			size = len(field.constant)
		}
		layout.wire = append(layout.wire, '}', ']')
		if err := builder.registry.addExpansion(&layout.measure,
			tableMeasure{bytes: size, values: 1, depth: 1}); err != nil {
			return layout, err
		}
	}
	layout.wire = append(layout.wire, ']')
	return layout, builder.registry.checkExpansion(layout.measure)
}
