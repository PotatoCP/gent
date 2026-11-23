package gent

import (
	"fmt"
	"reflect"
)

// A short type name can describe a large inline value, even in a nil container. Bound each
// derived array and each reflective allocation before constructing it. The logical JSON bound
// alone cannot detect this case. Writers use the same check so they do not save unreadable values.
// Direct Clone copies use it before allocation, even when no encoded-byte limit is set.
// This is not a total heap bound: maps have overhead, and custom codecs own their allocations.
func (r *CodecRegistry) checkValueAllocation(typ reflect.Type, count int) error {
	if count == 0 {
		return nil
	}
	limit, _ := r.expandedLimits()
	// Divide before multiplying. Both a nested array type and a container's element count can
	// overflow the address space if their sizes are multiplied without this check.
	if uint64(typ.Size()) > uint64(limit)/uint64(count) {
		return fmt.Errorf("gent: decoded allocation of %d %s values (%d bytes each) exceeds %d bytes",
			count, typ, typ.Size(), limit)
	}
	return nil
}
