package gent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

type ownedJSONValue struct{ data json.RawMessage }
type ownedJSONCodec struct{ received json.RawMessage }
type namedJSONCodec struct {
	ownedJSONCodec
	name string
}

func (codec *namedJSONCodec) Type() string { return codec.name }

func (*ownedJSONCodec) Type() string { return "test:owned-json" }
func (*ownedJSONCodec) Version() int { return 1 }
func (*ownedJSONCodec) Encode(value any) (json.RawMessage, error) {
	return value.(ownedJSONValue).data, nil
}
func (codec *ownedJSONCodec) Decode(data json.RawMessage) (any, error) {
	codec.received = data
	return ownedJSONValue{data}, nil
}

// Indexed decoding borrows one document. A custom codec must still receive the exact JSON of
// its value in an independent buffer, even when fields are reordered or strings contain brackets.
func TestCodecOwnsCustomJSONInput(t *testing.T) {
	type input struct{ kind, data string }
	type expected struct {
		value any
		err   error
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	const payload = `{"last": ["]}", {"quoted": "\\\"["}], "first": 9007199254740993}`
	cases := []testCase{
		{"direct", input{"test:owned-json", payload},
			expected{ownedJSONValue{json.RawMessage(payload)}, nil}},
		{"nested array", input{"slice", fmt.Sprintf(`{
  "values": [{"data": %s, "version": 1, "type": "test:owned-json"}],
  "element": "any"
}`, payload)}, expected{[]any{ownedJSONValue{json.RawMessage(payload)}}, nil}},
		{"nested map", input{"map", fmt.Sprintf(`{
  "items": [{"value": {"Data": %s, "VERSION": 1, "Type": "test:owned-json"},
    "key": {"data": "payload", "type": "/string", "version": 1}}],
  "key": "/string", "element": "any"
}`, payload)}, expected{map[string]any{"payload": ownedJSONValue{json.RawMessage(payload)}}, nil}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			codecs, codec := NewCodecRegistry(), &ownedJSONCodec{}
			if !assert.NoError(t, codecs.Register(ownedJSONValue{}, codec)) {
				return
			}
			encoded := EncodedValue{Type: test.input.kind, Version: 1,
				Data: json.RawMessage(test.input.data)}
			before := bytes.Clone(encoded.Data)
			value, err := codecs.Decode(encoded)
			if !assert.Equal(t, test.expected, expected{value, err}) {
				return
			}
			clear(encoded.Data)
			assert.Equal(t, json.RawMessage(payload), codec.received)
			copy(encoded.Data, before)
			codec.received[0] = '?'
			assert.Equal(t, before, []byte(encoded.Data))
		})
	}
}

func TestCodecPreservesEnvelopeJSONRules(t *testing.T) {
	type input struct{ kind, data string }
	type expected struct {
		value any
		err   error
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"null keeps a scalar field", input{"slice", `{
  "element": "/int", "values": [{"type": "/int", "version": 1,
    "VERSION": null, "data": 7}]
}`}, expected{[]int{7}, nil}},
		{"null keeps a boolean field", input{"slice", `{
  "element": "/int", "nil": true, "NIL": null
}`}, expected{[]int(nil), nil}},
		{"escaped field names and duplicate scalar", input{"slice", `{
  "values": [{"type": "/uint64", "version": 1, "data": 0,
    "d\u0061ta": 18446744073709551615}],
  "future": {"ignored": ["\"{[\\", []]}, "element": "/uint64"
}`}, expected{[]uint64{^uint64(0)}, nil}},
		{"duplicate object fields merge", input{"map", `{
  "items": [{"key": {"type": "/string", "version": 1, "data": "old"},
    "KEY": {"data": "current"},
    "value": {"type": "/int", "version": 1, "data": 7}}],
  "key": "/string", "element": "/int"
}`}, expected{map[string]int{"current": 7}, nil}},
		{"duplicate arrays and Unicode case folding", input{"slice", `{
  "values": [{"type": "/int", "version": 1, "data": 3}],
  "valueſ": [{"type": "/int", "version": 1, "data": 7}], "element": "/int"
}`}, expected{[]int{7}, nil}},
		{"stream envelope merge", input{"github.com/rickchristie/gent/StreamChunk", `{
  "content": {"type": "slice", "version": 1},
  "Content": {"data": {"element": "content", "nil": true}},
  "sourceMessageIds": ["old"], "ſourceMessageIds": ["accepted"]
}`}, expected{StreamChunk{SourceMessageIds: []string{"accepted"}}, nil}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			version := 1
			if test.input.kind == "github.com/rickchristie/gent/StreamChunk" {
				version = 4
			}
			value, err := NewCodecRegistry().Decode(EncodedValue{Type: test.input.kind,
				Version: version, Data: json.RawMessage(test.input.data)})
			assert.Equal(t, test.expected, expected{value, err})
		})
	}
}

func TestCodecMetadataStrings(t *testing.T) {
	type input struct{ encodedName, codecName string }
	type expected struct {
		value any
		err   error
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	want := expected{[]any{ownedJSONValue{json.RawMessage(`{}`)}}, nil}
	cases := []testCase{
		{"plain", input{`"test:plain"`, "test:plain"}, want},
		{"Unicode", input{`"test:🚀"`, "test:🚀"}, want},
		{"escaped", input{`"test:\ud83d\ude80"`, "test:🚀"}, want},
		{"invalid UTF-8", input{string([]byte{'"', 't', 'e', 's', 't', ':', 255, '"'}),
			"test:�"}, want},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			codecs := NewCodecRegistry()
			if !assert.NoError(t, codecs.Register(ownedJSONValue{},
				&namedJSONCodec{name: test.input.codecName})) {
				return
			}
			value, err := codecs.Decode(EncodedValue{Type: "slice", Version: 1,
				Data: json.RawMessage(fmt.Sprintf(`{"element":"any","values":[
{"type":%s,"version":1,"data":{}}]}`, test.input.encodedName))})
			assert.Equal(t, test.expected, expected{value, err})
		})
	}
}

// Compare arbitrary valid custom payloads with the standard decoder. The outer typed array
// forces the index to find the nested data boundary, including quoted delimiters and escapes.
func FuzzCodecNestedJSON(f *testing.F) {
	type jsonItem struct{ Data json.RawMessage }
	type jsonArray struct{ Values []jsonItem }
	for _, seed := range []string{
		`null`, `true`, `9007199254740993`, `-1.25e-10`,
		`{"array":[{},[],null,"\\\"{}[]"],"escaped\u006bey":"\u2028"}`,
		" " + strings.Repeat("[", 200) + "0" + strings.Repeat("]", 200) + " ",
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if !json.Valid(data) {
			return
		}
		encoded := EncodedValue{Type: "slice", Version: 1, Data: append([]byte(`{
  "element":"any","values":[{"type":"test:owned-json","version":1,"data":`), data...)}
		encoded.Data = append(encoded.Data, []byte(`}]}`)...)
		var standard jsonArray
		if !assert.NoError(t, json.Unmarshal(encoded.Data, &standard)) {
			return
		}
		codecs := NewCodecRegistry()
		if !assert.NoError(t, codecs.Register(ownedJSONValue{}, &ownedJSONCodec{})) {
			return
		}
		value, err := codecs.Decode(encoded)
		assert.NoError(t, err)
		assert.Equal(t, []any{ownedJSONValue{standard.Values[0].Data}}, value)
	})
}

type codecReadFrame struct {
	Next    *codecReadFrame
	Message *ModelMessage
}

// Application state can nest messages inside typed containers. Increasing depth must not make
// the decoder scan and copy the same saved prompt once for each surrounding container.
func BenchmarkCodecSavedValueDecode(b *testing.B) {
	type input struct{ depth int }
	type testCase struct {
		name  string
		input input
	}
	cases := []testCase{
		{"depth 1", input{1}}, {"depth 8", input{8}}, {"depth 24", input{24}},
	}
	for _, test := range cases {
		b.Run(test.name, func(b *testing.B) {
			codecs := NewCodecRegistry()
			if err := codecs.RegisterValueType(reflect.TypeFor[codecReadFrame]()); err != nil {
				b.Fatal(err)
			}
			value := &codecReadFrame{Message: TextParts(ChatMessageTypeHuman,
				strings.Repeat("Keep the original task. ", 4096))}
			for i := 1; i < test.input.depth; i++ {
				value = &codecReadFrame{Next: value}
			}
			data, err := codecs.Marshal(value)
			if err != nil {
				b.Fatal(err)
			}
			var restored *codecReadFrame
			b.ReportAllocs()
			b.SetBytes(int64(len(data)))
			for b.Loop() {
				if err := codecs.Unmarshal(data, &restored); err != nil {
					b.Fatal(err)
				}
			}
			assert.Equal(b, value, restored)
		})
	}
}
