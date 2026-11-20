package gent

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"sort"
	"strconv"
)

// A node is a typed encoder value, a container, or an opaque codec leaf. Nodes are built in
// child-first order. Equal nodes use one internal ID, with exact comparison after hashing.
type tableBuildNode struct {
	kind     tableNodeKind
	layout   int
	raw      json.RawMessage
	children []int
	measure  tableMeasure
	minimum  int
	share    bool
	uses     int
	shared   int
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
	layouts       [][][]any
	layoutIDs     map[string]int
	schemas       map[tableLayoutKey]int
	measures      []tableMeasure
	literals      map[string]int
	literalMemory map[tableLiteralMemory]int
	nodes         []tableBuildNode
	nodeIDs       map[[32]byte][]int
}

func (encoder *valueEncoder) marshalDocument(value encodingValue) ([]byte, error) {
	builder := tableBuilder{registry: encoder.CodecRegistry, types: []string{},
		typeIDs: map[string]int{}, layouts: [][][]any{}, layoutIDs: map[string]int{},
		nodeIDs: map[[32]byte][]int{}, schemas: map[tableLayoutKey]int{}, literals: map[string]int{},
		literalMemory: map[tableLiteralMemory]int{}}
	root, err := builder.value(value)
	if err != nil {
		return nil, err
	}
	builder.selectShared(root)
	shared := []json.RawMessage{}
	for id := range builder.nodes {
		if builder.nodes[id].shared < 0 {
			continue
		}
		builder.nodes[id].shared = len(shared)
		shared = append(shared, builder.appendValue(nil, id, true))
	}
	data, err := json.Marshal(savedDocumentData{Types: builder.types, Layouts: builder.layouts,
		Shared: shared, Root: builder.appendValue(nil, root, false)})
	if err != nil {
		return nil, err
	}
	return json.Marshal(savedDocument{FormatVersion: savedDocumentVersion, Data: data,
		Digest: documentDigest(data)})
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

func (builder *tableBuilder) typeField(name, value string) []any {
	return []any{name, map[string]any{"type": builder.typeID(value)}}
}

func tableConstant(name string, value any) []any {
	return []any{name, map[string]any{"constant": value}}
}

// Layout construction runs once per schema in this document. Rebuilding field metadata at
// every occurrence would trade smaller files for unnecessary allocations and save latency.
func (builder *tableBuilder) layout(key tableLayoutKey, fields func() [][]any) (int, error) {
	if id, ok := builder.schemas[key]; ok {
		return id, nil
	}
	layout := fields()
	data, err := json.Marshal(layout)
	if err != nil {
		return 0, err
	}
	if id, ok := builder.layoutIDs[string(data)]; ok {
		builder.schemas[key] = id
		return id, nil
	}
	measure := tableMeasure{bytes: 2, values: 1, depth: 1}
	for index, field := range layout {
		name, err := json.Marshal(field[0])
		if err != nil {
			return 0, err
		}
		measure.bytes += len(name) + 1
		if index > 0 {
			measure.bytes++
		}
		if len(field) == 1 {
			continue
		}
		constant := field[1].(map[string]any)
		value, ok := constant["constant"]
		if !ok {
			value = builder.types[constant["type"].(int)]
		}
		data, err := json.Marshal(value)
		if err != nil {
			return 0, err
		}
		if err := builder.registry.addExpansion(&measure,
			tableMeasure{bytes: len(data), values: 1, depth: 1}); err != nil {
			return 0, err
		}
	}
	if err := builder.registry.checkExpansion(measure); err != nil {
		return 0, err
	}
	id := len(builder.layouts)
	builder.schemas[key], builder.layoutIDs[string(data)] = id, id
	builder.layouts = append(builder.layouts, layout)
	builder.measures = append(builder.measures, measure)
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

func (builder *tableBuilder) envelope(value encodingValue) (int, error) {
	key := tableLayoutKey{kind: tableValueLayout, name: value.Type,
		declared: value.DeclaredType, version: value.Version}
	id, err := builder.layout(key, func() [][]any {
		fields := [][]any{builder.typeField("type", value.Type)}
		if value.DeclaredType != "" {
			fields = append(fields, builder.typeField("declaredType", value.DeclaredType))
		}
		return append(fields, tableConstant("version", value.Version), []any{"data"})
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
	return builder.object(id, []int{child}, true)
}

func (builder *tableBuilder) fields(name string, values map[string]encodingValue) (int, error) {
	id, err := builder.layout(tableLayoutKey{kind: tableFieldsLayout, name: name}, func() [][]any {
		names := make([]string, 0, len(values))
		for name := range values {
			names = append(names, name)
		}
		sort.Strings(names)
		fields := make([][]any, len(names))
		for i, name := range names {
			fields[i] = []any{name}
		}
		return fields
	})
	if err != nil {
		return 0, err
	}
	fields := builder.layouts[id]
	children := make([]int, len(fields))
	for i, field := range fields {
		children[i], err = builder.value(values[field[0].(string)])
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
		values := make([]any, len(value))
		for i := range value {
			values[i] = value[i]
		}
		return builder.array(values)
	case encodedMapItem[encodingValue]:
		id, err := builder.layout(tableLayoutKey{kind: tableMapItemLayout}, func() [][]any {
			return [][]any{{"key"}, {"value"}}
		})
		if err != nil {
			return 0, err
		}
		key, err := builder.value(value.Key)
		if err != nil {
			return 0, err
		}
		item, err := builder.value(value.Value)
		if err != nil {
			return 0, err
		}
		return builder.object(id, []int{key, item}, false)
	case []encodedMapItem[encodingValue]:
		values := make([]any, len(value))
		for i := range value {
			values[i] = value[i]
		}
		return builder.array(values)
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

func (builder *tableBuilder) container(value encodedContainer[encodingValue]) (int, error) {
	key := tableLayoutKey{kind: tableContainerLayout, name: value.Element, declared: value.Key,
		nilContainer: value.Nil, hasValues: len(value.Values) > 0, hasItems: len(value.Items) > 0}
	id, err := builder.layout(key, func() [][]any {
		fields := [][]any{}
		if value.Element != "" {
			fields = append(fields, builder.typeField("element", value.Element))
		}
		if value.Key != "" {
			fields = append(fields, builder.typeField("key", value.Key))
		}
		if value.Nil {
			fields = append(fields, tableConstant("nil", true))
		}
		if len(value.Values) > 0 {
			fields = append(fields, []any{"values"})
		}
		if len(value.Items) > 0 {
			fields = append(fields, []any{"items"})
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
		hasSources: len(value.SourceMessageIds) > 0}
	id, err := builder.layout(key, func() [][]any {
		fields := [][]any{}
		if value.Content != nil {
			fields = append(fields, []any{"content"})
		}
		if len(value.SourceMessageIds) > 0 {
			fields = append(fields, []any{"sourceMessageIds"})
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
	if len(value.SourceMessageIds) > 0 {
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
	indexed, err := indexSavedJSON(data)
	if err != nil {
		return 0, err
	}
	measure, err := builder.registry.measureLiteral(indexed, 0)
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

func (builder *tableBuilder) array(values []any) (int, error) {
	node := tableBuildNode{kind: tableArray, shared: -1, minimum: 4,
		measure: tableMeasure{bytes: 2, values: 1, depth: 1}}
	for i, value := range values {
		child, err := builder.value(value)
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
	key := append([]byte(node.kind), 0)
	key = binary.AppendVarint(key, int64(node.layout))
	for _, child := range node.children {
		key = binary.AppendUvarint(key, uint64(child))
	}
	hasher := sha256.New()
	hasher.Write(key)
	hasher.Write(node.raw)
	var hash [sha256.Size]byte
	hasher.Sum(hash[:0])
	for _, id := range builder.nodeIDs[hash] {
		other := &builder.nodes[id]
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
	builder.nodes = append(builder.nodes, node)
	builder.nodeIDs[hash] = append(builder.nodeIDs[hash], id)
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
