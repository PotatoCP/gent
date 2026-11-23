package gent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

const oversizedArrayDocument = `{
  "types":["slice"],
  "layouts":[[["type",{"type":0}],["version",{"constant":1}],["data"]]],
  "shared":[],
  "root":[0,[-3,{"element":"[1000000][1000000][1000000][1000000]/int","nil":true}]]
}`

func TestCodecRejectsOversizedSavedTypes(t *testing.T) {
	type input struct{ kind, data string }
	type expected struct{ errorText string }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	intBytes := reflect.TypeFor[int]().Size()
	arrayError := fmt.Sprintf(
		"gent: decoded allocation of 64 [64]int values (%d bytes each) exceeds 4096 bytes",
		64*intBytes)
	containerError := fmt.Sprintf(
		"gent: decoded allocation of 3 [512]int values (%d bytes each) exceeds 4096 bytes",
		512*intBytes)
	const nilValue = `{"type":"nil","version":1,"data":null}`
	values := `"values":[` + nilValue + `,` + nilValue + `,` + nilValue + `]`
	item := `{"key":` + nilValue + `,"value":` + nilValue + `}`
	items := `"items":[` + item + `,` + item + `,` + item + `]`
	cases := []testCase{
		{"type depth", input{"slice",
			`{"element":"` + strings.Repeat("*", 101) + `/int","nil":true}`},
			expected{"gent: type nesting exceeds 100"}},
		{"negative array length", input{"slice", `{"element":"[-1]/int","nil":true}`},
			expected{"gent: invalid array size"}},
		{"array length overflow", input{"slice",
			`{"element":"[18446744073709551616]/int","nil":true}`},
			expected{"gent: invalid array size"}},
		{"address size overflow", input{"slice",
			`{"element":"[1000000][1000000][1000000][1000000]/int","nil":true}`},
			expected{fmt.Sprintf(
				"gent: decoded allocation of 1000000 int values (%d bytes each) exceeds 4096 bytes",
				intBytes)}},
		{"nested array", input{"slice", `{"element":"[64][64]/int","nil":true}`},
			expected{arrayError}},
		{"nested pointer", input{"slice", `{"element":"*[64][64]/int","nil":true}`},
			expected{arrayError}},
		{"nested slice", input{"slice", `{"element":"[][64][64]/int","nil":true}`},
			expected{arrayError}},
		{"nested map value", input{"slice",
			`{"element":"map:[\"/string\",\"[64][64]/int\"]","nil":true}`},
			expected{arrayError}},
		{"nested map key", input{"slice",
			`{"element":"map:[\"[64][64]/int\",\"/string\"]","nil":true}`},
			expected{arrayError}},
		{"array allocation", input{"array", `{"element":"[512]/int",` + values + `}`},
			expected{containerError}},
		{"slice allocation", input{"slice", `{"element":"[512]/int",` + values + `}`},
			expected{containerError}},
		{"map value allocation", input{"map",
			`{"element":"[512]/int","key":"/string",` + items + `}`},
			expected{containerError}},
		{"map key allocation", input{"map",
			`{"element":"/string","key":"[512]/int",` + items + `}`},
			expected{containerError}},
		{"invalid map key", input{"slice",
			`{"element":"map:[\"[]/int\",\"/string\"]","nil":true}`},
			expected{"gent: map key is not comparable"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			codecs := NewCodecRegistry(WithCodecLimits(CodecLimits{
				MaxExpandedBytes: 4096, MaxExpandedValues: 128}))
			value := EncodedValue{Type: test.input.kind, Version: 1,
				Data: json.RawMessage(test.input.data)}
			data := savedTypeDocument(t, value)
			if !assert.NotPanics(t, func() {
				decoded, err := codecs.Decode(value)
				assert.EqualError(t, err, test.expected.errorText)
				assert.Nil(t, decoded)
				var target any = "unchanged target"
				err = codecs.Unmarshal(data, &target)
				assert.EqualError(t, err, test.expected.errorText)
				assert.Equal(t, "unchanged target", target)
				result, err := codecs.UnmarshalRunState(savedTypeRunState(t, data))
				assert.NoError(t, err)
				assert.Equal(t, &RunStateLoadResult{Status: RunStateInvalid,
					Detail: test.expected.errorText}, result)
			}) {
				return
			}
		})
	}
}

// Create valid storage integrity around malformed typed content. Logical JSON is small; the
// failure must come from checking the requested Go type or allocation before reflection uses it.
func savedTypeDocument(t *testing.T, value EncodedValue) []byte {
	t.Helper()
	data := fmt.Sprintf(`{"types":[%q],"layouts":[[["type",{"type":0}],
["version",{"constant":1}],["data"]]],"shared":[],"root":[0,[-3,%s]]}`,
		value.Type, value.Data)
	return testDocument(t, data)
}

func savedTypeRunState(t *testing.T, data []byte) []byte {
	t.Helper()
	digest := sha256.Sum256(data)
	result, err := json.Marshal(runStateSnapshotEncoding{SchemaVersion: RunSchemaVersion,
		State: data, Digest: hex.EncodeToString(digest[:])})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

type allocationEmpty struct{}

func TestCodecArraySizeBound(t *testing.T) {
	type input struct{ value any }
	type expected struct {
		value     any
		errorText string
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"exact size", input{[][4096]byte(nil)}, expected{value: [][4096]byte(nil)}},
		{"one byte too large", input{[][4097]byte(nil)}, expected{
			errorText: "gent: decoded allocation of 1 [4097]uint8 values (4097 bytes each) " +
				"exceeds 4096 bytes"}},
		{"zero size", input{[][1024][1024]allocationEmpty(nil)},
			expected{value: [][1024][1024]allocationEmpty(nil)}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			codecs := NewCodecRegistry(WithCodecLimits(CodecLimits{MaxExpandedBytes: 4096}))
			assert.NoError(t, codecs.RegisterValueType(reflect.TypeFor[allocationEmpty]()))
			data, err := codecs.Marshal(test.input.value)
			var actual expected
			if err != nil {
				actual.errorText = err.Error()
				assert.Nil(t, data)
			} else {
				assert.NoError(t, codecs.Unmarshal(data, &actual.value))
			}
			assert.Equal(t, test.expected, actual)
		})
	}
}
