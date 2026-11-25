package gent

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"hash/maphash"
	"sort"
	"strconv"
	"sync"
)

// A node is a typed encoder value, a container, or an opaque codec leaf. Nodes are built in
// child-first order. Equal nodes use one internal ID, with exact comparison after hashing.
type tableBuildNode struct {
	kind      tableNodeKind
	layout    int
	raw       json.RawMessage
	children  []int
	measure   tableMeasure
	minimum   int
	share     bool
	uses      int
	shared    int
	collision int
	written   int
}

type tableLayoutKind string

const (
	// tableValueLayout describes a complete typed value envelope.
	tableValueLayout tableLayoutKind = "value"
	// tableFieldsLayout describes the declared fields of a registered struct.
	tableFieldsLayout tableLayoutKind = "fields"
	// tableContainerLayout describes a pointer, array, slice, or map container.
	tableContainerLayout tableLayoutKind = "container"
	// tableChunkLayout describes the framework stream payload.
	tableChunkLayout tableLayoutKind = "chunk"
	// tableMapItemLayout describes one saved key and value pair.
	tableMapItemLayout tableLayoutKind = "map_item"
)

type tableLayoutKey struct {
	kind         tableLayoutKind
	name         string
	declared     string
	version      int
	nilContainer bool
	hasValues    bool
	hasItems     bool
	hasContent   bool
	hasSources   bool
}

type tableLiteralMemory struct {
	start  *byte
	length int
}

type tableBuilder struct {
	registry      *CodecRegistry
	types         []string
	typeIDs       map[string]int
	layouts       []tableBuildLayout
	layoutIDs     map[string]int
	schemas       map[tableLayoutKey]int
	measures      []tableMeasure
	literals      map[string]int
	literalMemory map[tableLiteralMemory]int
	nodes         []tableBuildNode
	nodeIDs       map[uint64]int
	indices       []int
}

// Reuse bounded scratch indexes across calls, never saved output or application values. Large
// archives do not set the idle memory cost of later small event encodings.
var tableBuilders = sync.Pool{New: func() any {
	return &tableBuilder{typeIDs: map[string]int{}, layoutIDs: map[string]int{},
		nodeIDs: map[uint64]int{}, schemas: map[tableLayoutKey]int{}, literals: map[string]int{},
		literalMemory: map[tableLiteralMemory]int{}}
}}

var tableNodeHashSeed = maphash.MakeSeed()

func (builder *tableBuilder) release() {
	if cap(builder.nodes) > 2048 || cap(builder.layouts) > 256 || cap(builder.types) > 256 ||
		len(builder.schemas) > 256 || len(builder.literals) > 2048 || len(builder.literalMemory) > 2048 ||
		cap(builder.indices) > 16384 {
		return
	}
	// Clear every reference before pooling. In particular, literalMemory keys can otherwise
	// keep a large provider buffer alive after the returned document has been released.
	builder.registry = nil
	clear(builder.types)
	clear(builder.layouts)
	clear(builder.nodes)
	clear(builder.typeIDs)
	clear(builder.layoutIDs)
	clear(builder.nodeIDs)
	clear(builder.schemas)
	clear(builder.literals)
	clear(builder.literalMemory)
	builder.types = builder.types[:0]
	builder.layouts = builder.layouts[:0]
	builder.nodes = builder.nodes[:0]
	builder.measures = builder.measures[:0]
	builder.indices = builder.indices[:0]
	tableBuilders.Put(builder)
}

func (encoder *valueEncoder) marshalDocument(value encodingValue) ([]byte, error) {
	builder := tableBuilders.Get().(*tableBuilder)
	builder.registry = encoder.CodecRegistry
	defer builder.release()
	root, err := builder.value(value)
	if err != nil {
		return nil, err
	}
	builder.selectShared(root)
	builder.assignShared()
	return builder.document(root)
}

func (builder *tableBuilder) document(root int) ([]byte, error) {
	// Append already validated nodes directly. Passing these buffers through json.Marshal
	// again would scan and copy the complete saved data at each outer envelope.
	types, err := json.Marshal(builder.types)
	if err != nil {
		return nil, err
	}
	data := make([]byte, 0, builder.dataSize(root, len(types)))
	data = append(data, `{"types":`...)
	data = append(data, types...)
	data = append(data, `,"layouts":[`...)
	for i, layout := range builder.layouts {
		if i > 0 {
			data = append(data, ',')
		}
		data = append(data, layout.wire...)
	}
	data = append(data, `],"shared":[`...)
	shared := 0
	for id := range builder.nodes {
		if builder.nodes[id].shared < 0 {
			continue
		}
		if shared > 0 {
			data = append(data, ',')
		}
		shared++
		data = builder.appendValue(data, id, true)
	}
	data = append(data, `],"root":`...)
	data = builder.appendValue(data, root, false)
	data = append(data, '}')
	data = builder.registry.compressDocument(data)
	result := make([]byte, 0, v3EnvelopeSize+len(data))
	result = append(result, v3DocumentPrefix...)
	result = append(result, data...)
	result = append(result, `,"digest":"`...)
	result = append(result, v3DocumentDigest(data)...)
	return append(result, '"', '}'), nil
}

// Shared IDs follow child-first order for both counting and writing. Counting must use the
// exact same reference widths as storage, including the transition to multi-digit IDs.
func (builder *tableBuilder) assignShared() {
	shared := 0
	for id := range builder.nodes {
		if builder.nodes[id].shared >= 0 {
			builder.nodes[id].shared = shared
			shared++
		}
	}
}

func (builder *tableBuilder) typeID(name string) int {
	if id, ok := builder.typeIDs[name]; ok {
		return id
	}
	id := len(builder.types)
	builder.types = append(builder.types, name)
	builder.typeIDs[name] = id
	return id
}

// Layout construction runs once per schema in this document. Typed fields avoid allocating
// interface slices and maps for each fixed metadata field of each streamed event.
func (builder *tableBuilder) layout(
	key tableLayoutKey, fields func() []tableBuildField,
) (int, error) {
	if id, ok := builder.schemas[key]; ok {
		return id, nil
	}
	layout, err := builder.buildLayout(fields())
	if err != nil {
		return 0, err
	}
	if id, ok := builder.layoutIDs[string(layout.wire)]; ok {
		builder.schemas[key] = id
		return id, nil
	}
	id := len(builder.layouts)
	builder.schemas[key], builder.layoutIDs[string(layout.wire)] = id, id
	builder.layouts = append(builder.layouts, layout)
	builder.measures = append(builder.measures, layout.measure)
	return id, nil
}

func (builder *tableBuilder) object(id int, children []int, share bool) (int, error) {
	node := tableBuildNode{kind: tableRecord, layout: id, share: share,
		minimum: 2 + len(strconv.Itoa(id)), measure: builder.measures[id], shared: -1, children: children}
	for _, child := range children {
		node.minimum += 1 + builder.minimumChild(child)
		if err := builder.registry.addExpansion(&node.measure,
			builder.nodes[child].measure); err != nil {
			return 0, err
		}
	}
	return builder.intern(node), nil
}

// Nodes borrow integer ranges until this document is finished. Only these scratch indexes are
// pooled; saved bytes and mutable application values never borrow this storage.
func (builder *tableBuilder) reserveChildren(count int) []int {
	start := len(builder.indices)
	builder.indices = append(builder.indices, make([]int, count)...)
	end := len(builder.indices)
	return builder.indices[start:end:end]
}

func (builder *tableBuilder) copyChildren(values ...int) []int {
	children := builder.reserveChildren(len(values))
	copy(children, values)
	return children
}

func (builder *tableBuilder) envelope(value encodingValue) (int, error) {
	key := tableLayoutKey{kind: tableValueLayout, name: value.Type,
		declared: value.DeclaredType, version: value.Version}
	id, err := builder.layout(key, func() []tableBuildField {
		fields := []tableBuildField{builder.typeField("type", value.Type)}
		if value.DeclaredType != "" {
			fields = append(fields, builder.typeField("declaredType", value.DeclaredType))
		}
		return append(fields, tableConstant("version", strconv.Itoa(value.Version)), tableField("data"))
	})
	if err != nil {
		return 0, err
	}
	var child int
	if fields, ok := value.Data.(map[string]encodingValue); ok {
		child, err = builder.fields(value.Type, fields)
	} else {
		child, err = builder.value(value.Data)
	}
	if err != nil {
		return 0, err
	}
	return builder.object(id, builder.copyChildren(child), true)
}

func (builder *tableBuilder) fields(name string, values map[string]encodingValue) (int, error) {
	key := tableLayoutKey{kind: tableFieldsLayout, name: name}
	id, err := builder.layout(key, func() []tableBuildField {
		names := make([]string, 0, len(values))
		for name := range values {
			names = append(names, name)
		}
		sort.Strings(names)
		fields := make([]tableBuildField, len(names))
		for i, name := range names {
			fields[i] = tableField(name)
		}
		return fields
	})
	if err != nil {
		return 0, err
	}
	fields := builder.layouts[id].fields
	children := builder.reserveChildren(len(fields))
	for i, field := range fields {
		children[i], err = builder.envelope(values[field.name])
		if err != nil {
			return 0, err
		}
	}
	return builder.object(id, children, false)
}

func (builder *tableBuilder) value(value any) (int, error) {
	switch value := value.(type) {
	case encodingValue:
		return builder.envelope(value)
	case EncodedValue:
		return builder.envelope(encodingValue{Type: value.Type, DeclaredType: value.DeclaredType,
			Version: value.Version, Data: value.Data})
	case encodedContainer[encodingValue]:
		return builder.container(value)
	case savedStreamChunk:
		return builder.chunk(value)
	case []encodingValue:
		return buildTableArray(builder, value, builder.envelope)
	case encodedMapItem[encodingValue]:
		return builder.mapItem(value)
	case []encodedMapItem[encodingValue]:
		return buildTableArray(builder, value, builder.mapItem)
	case json.RawMessage:
		return builder.literal(value)
	default:
		data, err := json.Marshal(value)
		if err != nil {
			return 0, err
		}
		return builder.literal(data)
	}
}

func (builder *tableBuilder) mapItem(value encodedMapItem[encodingValue]) (int, error) {
	id, err := builder.layout(tableLayoutKey{kind: tableMapItemLayout}, func() []tableBuildField {
		return []tableBuildField{tableField("key"), tableField("value")}
	})
	if err != nil {
		return 0, err
	}
	key, err := builder.envelope(encodingValue{Type: value.Key.Type,
		DeclaredType: value.Key.DeclaredType, Version: value.Key.Version, Data: value.Key.Data})
	if err != nil {
		return 0, err
	}
	item, err := builder.envelope(value.Value)
	if err != nil {
		return 0, err
	}
	return builder.object(id, builder.copyChildren(key, item), false)
}

func (builder *tableBuilder) container(value encodedContainer[encodingValue]) (int, error) {
	key := tableLayoutKey{kind: tableContainerLayout, name: value.Element, declared: value.Key,
		nilContainer: value.Nil, hasValues: len(value.Values) > 0, hasItems: len(value.Items) > 0}
	id, err := builder.layout(key, func() []tableBuildField {
		fields := []tableBuildField{}
		if value.Element != "" {
			fields = append(fields, builder.typeField("element", value.Element))
		}
		if value.Key != "" {
			fields = append(fields, builder.typeField("key", value.Key))
		}
		if value.Nil {
			fields = append(fields, tableConstant("nil", "true"))
		}
		if len(value.Values) > 0 {
			fields = append(fields, tableField("values"))
		}
		if len(value.Items) > 0 {
			fields = append(fields, tableField("items"))
		}
		return fields
	})
	if err != nil {
		return 0, err
	}
	var children []int
	if len(value.Values) > 0 {
		child, err := builder.value(value.Values)
		if err != nil {
			return 0, err
		}
		children = append(children, child)
	}
	if len(value.Items) > 0 {
		child, err := builder.value(value.Items)
		if err != nil {
			return 0, err
		}
		children = append(children, child)
	}
	return builder.object(id, children, false)
}

func (builder *tableBuilder) chunk(value savedStreamChunk) (int, error) {
	key := tableLayoutKey{kind: tableChunkLayout, hasContent: value.Content != nil,
		hasSources: value.SourceMessageIds != nil}
	id, err := builder.layout(key, func() []tableBuildField {
		fields := []tableBuildField{}
		if value.Content != nil {
			fields = append(fields, tableField("content"))
		}
		if value.SourceMessageIds != nil {
			fields = append(fields, tableField("sourceMessageIds"))
		}
		return fields
	})
	if err != nil {
		return 0, err
	}
	var children []int
	if value.Content != nil {
		child, err := builder.value(*value.Content)
		if err != nil {
			return 0, err
		}
		children = append(children, child)
	}
	if value.SourceMessageIds != nil {
		child, err := builder.value(value.SourceMessageIds)
		if err != nil {
			return 0, err
		}
		children = append(children, child)
	}
	return builder.object(id, children, false)
}

func (builder *tableBuilder) literal(data json.RawMessage) (int, error) {
	// The typed encoder owns stable leaf buffers during this call. Reuse an identical byte
	// range without copying, scanning, and hashing it again. This cache is not a wire identity
	// and never creates shared mutable decoded objects.
	var memory tableLiteralMemory
	if len(data) > 64 {
		memory = tableLiteralMemory{start: &data[0], length: len(data)}
		if id, ok := builder.literalMemory[memory]; ok {
			return id, nil
		}
	}
	if len(data) <= 64 {
		if id, ok := builder.literals[string(data)]; ok {
			return id, nil
		}
	}
	original := data
	// Match encoding/json's treatment of a codec leaf without interpreting its schema.
	// Native json.RawMessage fields are already saved as exact base64 bytes by the value codec.
	var err error
	data, err = json.Marshal(data)
	if err != nil {
		return 0, err
	}
	measure, err := builder.measureLiteral(data)
	if err != nil {
		return 0, err
	}
	id := builder.intern(tableBuildNode{kind: tableLiteral, raw: data, measure: measure,
		minimum: len(data), shared: -1})
	if len(original) <= 64 {
		builder.literals[string(original)] = id
	} else {
		builder.literalMemory[memory] = id
	}
	return id, nil
}

func (builder *tableBuilder) measureLiteral(data json.RawMessage) (tableMeasure, error) {
	// json.Marshal has already validated and normalized this leaf. A scalar has no child
	// values to index. Objects and arrays still need complete depth and expansion checks.
	if data[0] != '{' && data[0] != '[' {
		measure := tableMeasure{bytes: len(data), values: 1, depth: 1}
		return measure, builder.registry.checkExpansion(measure)
	}
	indexed, err := indexSavedJSON(data)
	if err != nil {
		return tableMeasure{}, err
	}
	return builder.registry.measureLiteral(indexed, 0)
}

// Keep typed children typed until their table nodes are built. An intermediate []any would
// allocate and box every envelope before the same traversal, including every stream event.
func buildTableArray[T any](
	builder *tableBuilder, values []T, encode func(T) (int, error),
) (int, error) {
	node := tableBuildNode{kind: tableArray, shared: -1, minimum: 4,
		measure:  tableMeasure{bytes: 2, values: 1, depth: 1},
		children: builder.reserveChildren(min(len(values), codecInitialCapacity))[:0]}
	for i, value := range values {
		child, err := encode(value)
		if err != nil {
			return 0, err
		}
		node.children = append(node.children, child)
		node.minimum += 1 + builder.minimumChild(child)
		if i > 0 {
			node.measure.bytes++
		}
		if err := builder.registry.addExpansion(&node.measure,
			builder.nodes[child].measure); err != nil {
			return 0, err
		}
	}
	return builder.intern(node), nil
}

func (builder *tableBuilder) intern(node tableBuildNode) int {
	// This hash only selects exact-comparison candidates within one encode call. It is not
	// an integrity digest or a saved identity. Full comparison below handles collisions.
	var hasher maphash.Hash
	hasher.SetSeed(tableNodeHashSeed)
	hasher.WriteString(string(node.kind))
	var buffer [binary.MaxVarintLen64]byte
	count := binary.PutVarint(buffer[:], int64(node.layout))
	hasher.Write(buffer[:count])
	for _, child := range node.children {
		count = binary.PutUvarint(buffer[:], uint64(child))
		hasher.Write(buffer[:count])
	}
	hasher.Write(node.raw)
	hash := hasher.Sum64()
	for next := builder.nodeIDs[hash]; next != 0; {
		id := next - 1
		other := &builder.nodes[id]
		next = other.collision
		if other.kind != node.kind || other.layout != node.layout || !bytes.Equal(other.raw, node.raw) ||
			len(other.children) != len(node.children) {
			continue
		}
		equal := true
		for i, child := range node.children {
			if other.children[i] != child {
				equal = false
				break
			}
		}
		if equal {
			other.share = other.share || node.share
			return id
		}
	}
	id := len(builder.nodes)
	node.collision = builder.nodeIDs[hash]
	builder.nodes = append(builder.nodes, node)
	builder.nodeIDs[hash] = id + 1
	return id
}

func (builder *tableBuilder) minimumChild(id int) int {
	node := &builder.nodes[id]
	if node.share {
		return min(node.minimum, 6)
	}
	return node.minimum
}

func (builder *tableBuilder) selectShared(root int) {
	builder.nodes[root].uses = 1
	// Parents come first here. Sharing a parent counts its children once, so nested sharing
	// cannot claim savings from copies which the shared parent has already removed.
	referenceBytes := 5 + len(strconv.Itoa(len(builder.nodes)))
	for id := len(builder.nodes) - 1; id >= 0; id-- {
		node := &builder.nodes[id]
		uses := node.uses
		// minimum is a lower bound even if every child becomes a short reference. Include the
		// table comma and use a conservative reference width. Only positive savings are selected.
		if node.share && uses > 1 && node.minimum > referenceBytes+1 &&
			(uses-1)*(node.minimum-referenceBytes) > referenceBytes+1 {
			node.shared = 0
			uses = 1
		}
		for _, child := range node.children {
			builder.nodes[child].uses += uses
		}
	}
}

func (builder *tableBuilder) appendValue(data []byte, id int, definition bool) []byte {
	node := &builder.nodes[id]
	if node.shared >= 0 && !definition {
		data = append(data, '[')
		data = strconv.AppendInt(data, int64(tableWireTag(tableReference)), 10)
		data = append(data, ',')
		data = strconv.AppendInt(data, int64(node.shared), 10)
		return append(data, ']')
	}
	if node.kind == tableLiteral {
		if len(node.raw) > 0 && (node.raw[0] == '{' || node.raw[0] == '[') {
			data = append(data, '[')
			data = strconv.AppendInt(data, int64(tableWireTag(tableLiteral)), 10)
			data = append(data, ',')
			data = append(data, node.raw...)
			return append(data, ']')
		}
		return append(data, node.raw...)
	}
	data = append(data, '[')
	tag := node.layout
	if node.kind == tableArray {
		tag = tableWireTag(tableArray)
	}
	data = strconv.AppendInt(data, int64(tag), 10)
	for _, child := range node.children {
		data = append(data, ',')
		data = builder.appendValue(data, child, false)
	}
	return append(data, ']')
}
