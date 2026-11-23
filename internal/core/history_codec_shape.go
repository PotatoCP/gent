package gent

import "reflect"

// Limit initial scratch storage before child validation. A very large slice of zero-size values
// can fail at its first child; its length must not request a large temporary encoding buffer.
// Supported larger values grow their storage only as the traversal reaches their children.
const codecInitialCapacity = 1024

// A registration owns an immutable description of its Go fields and scalar methods. Reusing this
// description avoids reflection allocations for every streamed value. It contains no application
// data, codec results, or limit decisions; those remain specific to each operation.
type codecShape struct {
	fields        []codecField
	valueCount    int
	scalarMethods bool
}

type codecField struct {
	name     string
	typ      reflect.Type
	private  bool
	callback bool
}

func newCodecShape(typ reflect.Type) *codecShape {
	shape := &codecShape{scalarMethods: scalarHasJSONMethods(typ)}
	if typ.Kind() != reflect.Struct {
		return shape
	}
	shape.fields = make([]codecField, typ.NumField())
	for i := range typ.NumField() {
		field := typ.Field(i)
		callback := field.Type.Kind() == reflect.Func && field.Tag.Get("json") == "-"
		shape.fields[i] = codecField{name: field.Name, typ: field.Type,
			private: field.PkgPath != "", callback: callback}
		if !callback {
			shape.valueCount++
		}
	}
	return shape
}

// Declarations can contain application registrations. Keep these names within one encode call,
// so a later operation sees any newly registered types without a shared cache invalidation rule.
func (encoder *valueEncoder) declaredFieldType(typ reflect.Type) string {
	if name, ok := encoder.declarations[typ]; ok {
		return name
	}
	name := encoder.CodecRegistry.declaredFieldType(typ)
	if encoder.declarations == nil {
		encoder.declarations = make(map[reflect.Type]string)
	}
	encoder.declarations[typ] = name
	return name
}
