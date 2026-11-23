package gent

import (
	"encoding/json"
	"reflect"
	"strconv"
)

// uncompressedSize validates and counts the plain document without allocating its final bytes.
// Pending events and subscriber queues keep their original uncompressed byte allowance. Storage
// compression must not allow larger live queues. Component codec and expansion checks still run.
func (registry *CodecRegistry) uncompressedSize(value any) (int, error) {
	encoder := newValueEncoder(registry)
	encoded, err := encoder.encode(reflect.ValueOf(value), 0)
	if err != nil {
		return 0, err
	}
	builder := tableBuilders.Get().(*tableBuilder)
	builder.registry = registry
	defer builder.release()
	root, err := builder.value(encoded)
	if err != nil {
		return 0, err
	}
	builder.selectShared(root)
	builder.assignShared()
	types, err := json.Marshal(builder.types)
	if err != nil {
		return 0, err
	}
	size := documentEnvelopeSize(savedDocumentVersion) + builder.dataSize(root, len(types))
	return size, registry.checkEncodedSize(size)
}

// Counting and storage use the same resolved layouts and shared IDs. This keeps queue limits
// exact and lets storage allocate its output once instead of repeatedly copying a growing buffer.
func (builder *tableBuilder) dataSize(root, typesSize int) int {
	size := len(`{"types":,"layouts":[],"shared":[],"root":}`) + typesSize
	for index, layout := range builder.layouts {
		size += len(layout.wire)
		if index > 0 {
			size++
		}
	}
	for id := range builder.nodes {
		node := &builder.nodes[id]
		node.written = builder.nodeSize(node)
		if node.shared < 0 {
			continue
		}
		size += node.written
		if node.shared > 0 {
			size++
		}
		node.written = 5 + len(strconv.Itoa(node.shared))
	}
	size += builder.nodes[root].written
	return size
}

func (builder *tableBuilder) nodeSize(node *tableBuildNode) int {
	if node.kind == tableLiteral {
		size := len(node.raw)
		if size > 0 && (node.raw[0] == '[' || node.raw[0] == '{') {
			size += 5 // [-3,<literal>]
		}
		return size
	}
	tag := node.layout
	if node.kind == tableArray {
		tag = tableWireTag(tableArray)
	}
	size := 2 + len(strconv.Itoa(tag))
	for _, id := range node.children {
		size += 1 + builder.nodes[id].written
	}
	return size
}
