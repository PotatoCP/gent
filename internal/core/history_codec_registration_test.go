package gent

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type registrationRecord struct{ Value string }

type registrationCodec struct{ id string }

func (codec registrationCodec) Type() string { return codec.id }
func (registrationCodec) Version() int       { return 1 }
func (registrationCodec) Encode(value any) (json.RawMessage, error) {
	return json.Marshal(value)
}
func (registrationCodec) Decode(data json.RawMessage) (any, error) {
	var value registrationRecord
	err := json.Unmarshal(data, &value)
	return value, err
}

func registerTestRecord(codecs *CodecRegistry, id string, custom bool) error {
	if custom {
		return codecs.Register(registrationRecord{}, registrationCodec{id: id})
	}
	return codecs.RegisterType(id, 1, registrationRecord{})
}

func TestCodecRegistrationNames(t *testing.T) {
	type input struct{ id string }
	type expected struct{ errorText string }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"application name", input{"app.record"}, expected{}},
		{"application path", input{"app/record"}, expected{}},
		{"application namespace", input{"app:record"}, expected{}},
		{"qualified reserved word", input{"app.nil"}, expected{}},
		{"internal type syntax", input{"app.[]/string"}, expected{}},
		{"map name without colon", input{"map.record"}, expected{}},
		{"Unicode name", input{"app.値"}, expected{}},
		{"replacement character", input{"app.�"}, expected{}},
		{"non-BMP character", input{"app.😀"}, expected{}},
		{"invalid byte", input{"app.\xff"}, expected{
			`gent: codec name "app.\xff" is not valid UTF-8`}},
		{"truncated sequence", input{"app.\xe2\x82"}, expected{
			`gent: codec name "app.\xe2\x82" is not valid UTF-8`}},
		{"invalid continuation", input{"app.\x80"}, expected{
			`gent: codec name "app.\x80" is not valid UTF-8`}},
		{"overlong sequence", input{"app.\xc0\xaf"}, expected{
			`gent: codec name "app.\xc0\xaf" is not valid UTF-8`}},
		{"surrogate sequence", input{"app.\xed\xa0\x80"}, expected{
			`gent: codec name "app.\xed\xa0\x80" is not valid UTF-8`}},
		{"above Unicode range", input{"app.\xf4\x90\x80\x80"}, expected{
			`gent: codec name "app.\xf4\x90\x80\x80" is not valid UTF-8`}},
	}
	for _, id := range []string{
		"bytes", "nil", "pointer", "slice", "array", "map", "time", "error", "encoded-value",
		"raw-json-bytes", "raw-json-nil", "any", "content", "error-interface",
		"*/string", "[]/string", "[1]/string", `map:["/string","/int"]`,
		"*", "[", "[]", "[unfinished", "map:", "map:unfinished",
	} {
		cases = append(cases, testCase{id, input{id}, expected{
			fmt.Sprintf("gent: codec name %q is reserved", id)}})
	}
	for _, test := range cases {
		for _, custom := range []bool{false, true} {
			for _, compression := range []CodecCompression{CodecCompressionNone, CodecCompressionSnappy} {
				name := fmt.Sprintf("%s/custom=%t/%s", test.name, custom, compression)
				t.Run(name, func(t *testing.T) {
					codecs := NewCodecRegistry(WithCodecCompression(compression))
					// A rejected name must not change previously saved data or reserve its Go type.
					original := registrationBuiltins()
					before, err := codecs.Marshal(original)
					if !assert.NoError(t, err) {
						return
					}
					err = registerTestRecord(codecs, test.input.id, custom)
					actual := expected{}
					if err != nil {
						actual.errorText = err.Error()
					}
					if !assert.Equal(t, test.expected, actual) {
						return
					}
					if err != nil && !assert.NoError(t, registerTestRecord(codecs, "app.record", custom)) {
						return
					}
					after, err := codecs.Marshal(original)
					assert.NoError(t, err)
					assert.Equal(t, before, after)
					var restored any
					assert.NoError(t, codecs.Unmarshal(before, &restored))
					assert.Equal(t, original, restored)
					assertRegistrationRoundTrip(t, codecs, original)
					record := registrationRecord{Value: strings.Repeat("accepted custom state ", 1024)}
					assertRegistrationRoundTrip(t, codecs, record)
					assertRegistrationRoundTrip(t, codecs, []registrationRecord{record})
				})
			}
		}
	}
}

func TestCodecRegistrationKeepsUnicodeIdentity(t *testing.T) {
	type input struct {
		id       string
		existing string
	}
	type expected struct{ errorText string }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	type otherRecord struct{ Value string }
	cases := []testCase{
		{"invalid byte collision", input{"app.\xff", "app.�"}, expected{
			`gent: codec name "app.\xff" is not valid UTF-8`}},
		{"truncated sequence collision", input{"app.\xe2\x82", "app.��"}, expected{
			`gent: codec name "app.\xe2\x82" is not valid UTF-8`}},
		{"distinct valid Unicode", input{"app.e\u0301", "app.é"}, expected{}},
	}
	for _, test := range cases {
		for _, custom := range []bool{false, true} {
			for _, compression := range []CodecCompression{CodecCompressionNone, CodecCompressionSnappy} {
				t.Run(fmt.Sprintf("%s/custom=%t/%s", test.name, custom, compression), func(t *testing.T) {
					codecs := NewCodecRegistry(WithCodecCompression(compression))
					if !assert.NoError(t, codecs.RegisterType(test.input.existing, 1, otherRecord{})) {
						return
					}
					other := otherRecord{Value: strings.Repeat("existing custom state ", 1024)}
					before, err := codecs.Marshal(other)
					if !assert.NoError(t, err) {
						return
					}
					err = registerTestRecord(codecs, test.input.id, custom)
					actual := expected{}
					if err != nil {
						actual.errorText = err.Error()
					}
					if !assert.Equal(t, test.expected, actual) {
						return
					}
					if err != nil && !assert.NoError(t, registerTestRecord(codecs, "app.値", custom)) {
						return
					}
					// Do not normalize names or repair malformed bytes into the existing binding.
					// Both saved identities must still select their original concrete Go types.
					after, err := codecs.Marshal(other)
					assert.NoError(t, err)
					assert.Equal(t, before, after)
					var restored any
					assert.NoError(t, codecs.Unmarshal(before, &restored))
					assert.Equal(t, other, restored)
					assertRegistrationRoundTrip(t, codecs, []any{other,
						registrationRecord{Value: "saved"}})
				})
			}
		}
	}
}

// Include the type aliases and each derived form inside a containing slice. A conflicting
// registration can leave root values readable while silently changing their container types.
func registrationBuiltins() []any {
	text := "saved value"
	return []any{
		nil, []byte(nil), []byte{}, []byte{0, 1, 255},
		time.Date(2026, time.November, 21, 0, 0, 0, 0, time.UTC),
		EncodedValue{Type: "app.payload", Version: 1, Data: json.RawMessage(`{"value":1}`)},
		json.RawMessage(nil), json.RawMessage(`{"value":1}`),
		[]any{nil, text}, []ContentPart{TextPart(text)},
		[]error{nil, &SavedError{Message: "saved error"}},
		[][]string{{text}}, []*string{&text}, [][1]string{{text}}, []map[string]int{{text: 1}},
	}
}

func assertRegistrationRoundTrip(t *testing.T, codecs *CodecRegistry, value any) {
	t.Helper()
	encoded, err := codecs.Encode(value)
	if !assert.NoError(t, err) {
		return
	}
	restored, err := codecs.Decode(encoded)
	assert.NoError(t, err)
	assert.Equal(t, value, restored)
	// Component envelopes are also stored inside checkpoint data. Their type names must keep
	// the same bytes through JSON storage before the component decoder selects a binding.
	component, err := codecs.Marshal(encoded)
	if !assert.NoError(t, err) {
		return
	}
	var loaded EncodedValue
	assert.NoError(t, codecs.Unmarshal(component, &loaded))
	assert.Equal(t, encoded, loaded)
	restored, err = codecs.Decode(loaded)
	assert.NoError(t, err)
	assert.Equal(t, value, restored)
	data, err := codecs.Marshal(value)
	if !assert.NoError(t, err) {
		return
	}
	restored = nil
	assert.NoError(t, codecs.Unmarshal(data, &restored))
	assert.Equal(t, value, restored)
}
