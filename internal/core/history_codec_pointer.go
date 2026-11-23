package gent

import "reflect"

// A zero-size target can use Go's shared zero address. Give it nonzero backing storage so two
// decoded pointers remain distinct map keys. The pointer keeps this backing value alive. No source
// address enters the saved representation, and the extra byte has no logical content.
func pointerStorageType(element reflect.Type) reflect.Type {
	if element.Size() != 0 {
		return element
	}
	return reflect.StructOf([]reflect.StructField{
		{Name: "Value", Type: element},
		{Name: "Padding", Type: reflect.TypeFor[byte]()},
	})
}

func (r *CodecRegistry) newPointerValue(element reflect.Type) (reflect.Value, error) {
	storage := pointerStorageType(element)
	if err := r.checkValueAllocation(storage, 1); err != nil {
		return reflect.Value{}, err
	}
	value := reflect.New(storage)
	if element.Size() == 0 {
		return value.Elem().Field(0).Addr(), nil
	}
	return value, nil
}
