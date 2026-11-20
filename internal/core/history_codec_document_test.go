package gent

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSavedDocumentCustomCodecOwnsEachOccurrence(t *testing.T) {
	registry, codec := NewCodecRegistry(), &ownedJSONCodec{}
	assert.NoError(t, registry.Register(ownedJSONValue{}, codec))
	payload := json.RawMessage(`{"original":"` + strings.Repeat("saved ", 200) + `"}`)
	original := []ownedJSONValue{{payload}, {payload}, {payload}}
	data, err := registry.Marshal(original)
	if !assert.NoError(t, err) {
		return
	}
	before := bytes.Clone(data)
	var values []ownedJSONValue
	assert.NoError(t, registry.Unmarshal(data, &values))
	assert.Equal(t, original, values)
	values[0].data[0] = '?'
	assert.Equal(t, original[1:], values[1:])
	assert.Equal(t, before, data)
	assert.Equal(t, byte('{'), payload[0])
}

func TestSavedDocumentValidatesBeforeCustomDecode(t *testing.T) {
	registry, codec := NewCodecRegistry(), &ownedJSONCodec{}
	assert.NoError(t, registry.Register(ownedJSONValue{}, codec))
	data, err := registry.Marshal(ownedJSONValue{json.RawMessage(`{"saved":true}`)})
	if !assert.NoError(t, err) {
		return
	}
	var document savedDocument
	assert.NoError(t, json.Unmarshal(data, &document))
	var body savedDocumentData
	assert.NoError(t, json.Unmarshal(document.Data, &body))
	// Even an unused malformed shared definition must be rejected before the root's codec.
	body.Shared = append(body.Shared, json.RawMessage(fmt.Sprintf(`[-2,%d]`, len(body.Shared))))
	document.Data, err = json.Marshal(body)
	assert.NoError(t, err)
	document.Digest = documentDigest(document.Data)
	data, err = json.Marshal(document)
	assert.NoError(t, err)
	var value ownedJSONValue
	assert.EqualError(t, registry.Unmarshal(data, &value),
		"gent: invalid or forward shared reference")
	assert.Equal(t, ownedJSONValue{}, value)
	assert.Nil(t, codec.received)
}

func TestSavedDocumentKeepsBatchReceipts(t *testing.T) {
	registry := NewCodecRegistry()
	_, batches := recordedStateFixture(t, 2, registry, nil)
	for i, data := range batches {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			var batch SaveBatch
			if !assert.NoError(t, registry.Unmarshal(data, &batch)) {
				return
			}
			copy := batch
			copy.BatchId = ""
			envelope, err := registry.Encode(&copy)
			assert.NoError(t, err)
			canonical, err := json.Marshal(envelope)
			assert.NoError(t, err)
			digest := sha256.Sum256(canonical)
			assert.Equal(t, hex.EncodeToString(digest[:]), batch.BatchId)
			actual, err := BatchDigest(&batch, registry)
			assert.NoError(t, err)
			assert.Equal(t, batch.BatchId, actual)
		})
	}
}

func TestSavedDocumentExpansionBoundary(t *testing.T) {
	type input struct{ delta int }
	type expected struct {
		value     any
		errorText string
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	registry := NewCodecRegistry()
	original := []string{strings.Repeat("preserved ", 64), strings.Repeat("preserved ", 64)}
	data, err := registry.Marshal(original)
	assert.NoError(t, err)
	expanded := expandTestDocument(t, registry, data)
	cases := []testCase{
		{"exact limit", input{}, expected{value: original}},
		{"one byte below", input{-1}, expected{
			errorText: fmt.Sprintf("gent: expanded value exceeds %d bytes", len(expanded)-1),
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			limited := NewCodecRegistry(WithCodecLimits(CodecLimits{
				MaxExpandedBytes: len(expanded) + test.input.delta,
			}))
			var value any
			err := limited.Unmarshal(data, &value)
			if test.expected.errorText == "" {
				assert.NoError(t, err)
				encoded, err := limited.Marshal(original)
				assert.NoError(t, err)
				assert.Equal(t, data, encoded)
			} else {
				assert.EqualError(t, err, test.expected.errorText)
				_, err = limited.Marshal(original)
				assert.EqualError(t, err, test.expected.errorText)
			}
			assert.Equal(t, test.expected.value, value)
		})
	}
}

func TestSavedDocumentRejectsDeepReferences(t *testing.T) {
	shared := []string{`null`}
	for id := range 512 {
		shared = append(shared, fmt.Sprintf(`[-1,[-2,%d]]`, id))
	}
	payload := fmt.Sprintf(`{"types":[],"layouts":[],"shared":[%s],"root":[-2,512]}`,
		strings.Join(shared, ","))
	var value any
	assert.EqualError(t, NewCodecRegistry().Unmarshal(testDocument(t, payload), &value),
		"gent: saved document nesting exceeds 512")
	assert.Nil(t, value)
}

func TestRunStateDocumentLoadResults(t *testing.T) {
	type input struct{ change func(*savedDocument) }
	type expected struct {
		result    *RunStateLoadResult
		errorText string
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"unsupported format", input{func(value *savedDocument) { value.FormatVersion++ }},
			expected{result: &RunStateLoadResult{Status: RunStateIncompatible,
				Detail: "gent: unsupported saved document version 2"}}},
		{"changed table", input{func(value *savedDocument) {
			value.Data = bytes.Replace(value.Data, []byte(`"run:document"`), []byte(`"run:changed"`), 1)
		}}, expected{result: &RunStateLoadResult{Status: RunStateInvalid,
			Detail: "gent: saved document digest mismatch"}}},
		{"invalid state with valid integrity", input{func(value *savedDocument) {
			value.Data = bytes.Replace(value.Data, []byte(`"run:document"`), []byte(`""`), 1)
			value.Digest = documentDigest(value.Data)
		}}, expected{result: &RunStateLoadResult{Status: RunStateInvalid,
			Detail: "gent: saved run ID is empty"}}},
		{"invalid reference with valid integrity", input{func(value *savedDocument) {
			value.Data = json.RawMessage(`{"types":[],"layouts":[],"shared":[],"root":[-2,0]}`)
			value.Digest = documentDigest(value.Data)
		}}, expected{result: &RunStateLoadResult{Status: RunStateInvalid,
			Detail: "gent: invalid or forward shared reference"}}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			registry := NewCodecRegistry()
			data, err := registry.MarshalRunState(NewRunState("run:document"))
			if !assert.NoError(t, err) {
				return
			}
			var outer runStateSnapshotEncoding
			assert.NoError(t, json.Unmarshal(data, &outer))
			var document savedDocument
			assert.NoError(t, json.Unmarshal(outer.State, &document))
			test.input.change(&document)
			outer.State, err = json.Marshal(document)
			assert.NoError(t, err)
			digest := sha256.Sum256(outer.State)
			outer.Digest = hex.EncodeToString(digest[:])
			data, err = json.Marshal(outer)
			assert.NoError(t, err)
			result, err := registry.UnmarshalRunState(data)
			actual := expected{result: result}
			if err != nil {
				actual.errorText = err.Error()
			}
			assert.Equal(t, test.expected, actual)
		})
	}
}

func TestFormerSavedEnvelopeIsIncompatible(t *testing.T) {
	registry := NewCodecRegistry()
	state := NewRunState("run:former")
	envelope, err := registry.Encode(state.data)
	assert.NoError(t, err)
	encoded, err := json.Marshal(envelope)
	assert.NoError(t, err)
	digest := sha256.Sum256(encoded)
	data, err := json.Marshal(runStateSnapshotEncoding{SchemaVersion: RunSchemaVersion,
		State: encoded, Digest: hex.EncodeToString(digest[:])})
	assert.NoError(t, err)
	result, err := registry.UnmarshalRunState(data)
	assert.NoError(t, err)
	assert.Equal(t, &RunStateLoadResult{Status: RunStateIncompatible,
		Detail: "gent: unsupported saved envelope format"}, result)
	var value any
	assert.EqualError(t, registry.Unmarshal(encoded, &value),
		"gent: unsupported saved envelope format")
	assert.Nil(t, value)
}

// Only tests expand a complete document. Compare every metadata field and payload byte with the
// component encoding to prove that storage savings do not omit recovery information.
func expandTestDocument(t *testing.T, codecs *CodecRegistry, data []byte) []byte {
	t.Helper()
	var document savedDocument
	if !assert.NoError(t, json.Unmarshal(data, &document)) {
		t.FailNow()
	}
	assert.Equal(t, documentDigest(document.Data), document.Digest)
	indexed, err := indexSavedJSON(document.Data)
	if !assert.NoError(t, err) {
		t.FailNow()
	}
	reader := tableReader{registry: codecs}
	root, err := reader.read(indexed)
	if !assert.NoError(t, err) {
		t.FailNow()
	}
	return root.bytes()
}

func testDocument(t *testing.T, payload string) []byte {
	t.Helper()
	var compact bytes.Buffer
	if !assert.NoError(t, json.Compact(&compact, []byte(payload))) {
		t.FailNow()
	}
	content := compact.Bytes()
	data, err := json.Marshal(savedDocument{FormatVersion: savedDocumentVersion,
		Data: content, Digest: documentDigest(content)})
	if !assert.NoError(t, err) {
		t.FailNow()
	}
	return data
}

func TestSavedDocumentFormat(t *testing.T) {
	codecs := NewCodecRegistry()
	data, err := codecs.Marshal(uint64(95))
	assert.NoError(t, err)
	payload := `{"types":["/uint64"],"layouts":[[["type",{"type":0}],` +
		`["version",{"constant":1}],["data"]]],"shared":[],"root":[0,95]}`
	assert.Equal(t, testDocument(t, payload), data)
	var value uint64
	assert.NoError(t, NewCodecRegistry().Unmarshal(data, &value))
	assert.Equal(t, uint64(95), value)
}

func TestSavedDocumentSharingOwnsEachOccurrence(t *testing.T) {
	type content struct {
		Text   string
		Values map[string][]byte
		Native json.RawMessage
	}
	codecs := NewCodecRegistry()
	assert.NoError(t, codecs.RegisterValueType(reflect.TypeFor[content]()))
	native := json.RawMessage(` { "id": "\u0061<&>", "value": 18446744073709551615 } `)
	item := &content{Text: strings.Repeat("keep this original input ", 128),
		Values: map[string][]byte{"buffer": bytes.Repeat([]byte{0, 255, 7}, 200)}, Native: native}
	original := []*content{item, item, item, item}
	data, err := codecs.Marshal(original)
	if !assert.NoError(t, err) {
		return
	}
	component, err := codecs.Encode(original)
	assert.NoError(t, err)
	expanded, err := json.Marshal(component)
	assert.NoError(t, err)
	assert.Equal(t, expanded, expandTestDocument(t, codecs, data))
	assert.Less(t, len(data), len(expanded)/2)
	var document savedDocument
	assert.NoError(t, json.Unmarshal(data, &document))
	var payload savedDocumentData
	assert.NoError(t, json.Unmarshal(document.Data, &payload))
	assert.NotEmpty(t, payload.Shared)
	var workers sync.WaitGroup
	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			var restored []*content
			if !assert.NoError(t, codecs.Unmarshal(data, &restored)) {
				return
			}
			assert.Equal(t, original, restored)
			restored[0].Text = "changed"
			restored[0].Values["buffer"][0] = 99
			restored[0].Native[0] = '\n'
			assert.Equal(t, original[1:], restored[1:])
			assert.Equal(t, byte(0), original[0].Values["buffer"][0])
		}()
	}
	workers.Wait()
	again, err := codecs.Marshal(original)
	assert.NoError(t, err)
	assert.Equal(t, data, again)
}

func TestSavedDocumentRejectsInvalidTables(t *testing.T) {
	type input struct{ payload string }
	type expected struct{ errorText string }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"missing reference", input{`{"types":[],"layouts":[],"shared":[],"root":[-2,0]}`},
			expected{"gent: invalid or forward shared reference"}},
		{"self reference", input{`{"types":[],"layouts":[],"shared":[[-2,0]],"root":null}`},
			expected{"gent: invalid or forward shared reference"}},
		{"forward reference", input{`{"types":[],"layouts":[],"shared":[[-2,1],1],"root":null}`},
			expected{"gent: invalid or forward shared reference"}},
		{"negative reference", input{`{"types":[],"layouts":[],"shared":[],"root":[-2,-1]}`},
			expected{"gent: invalid or forward shared reference"}},
		{"fractional reference", input{`{"types":[],"layouts":[],"shared":[1],"root":[-2,0.5]}`},
			expected{"json: cannot unmarshal number 0.5 into Go value of type int"}},
		{"missing layout", input{`{"types":[],"layouts":[],"shared":[],"root":[0]}`},
			expected{"gent: invalid layout reference"}},
		{"wrong slot count", input{`{"types":[],"layouts":[[["data"]]],"shared":[],"root":[0]}`},
			expected{"gent: layout value count differs from its slots"}},
		{"missing type", input{`{"types":[],"layouts":[[["type",{"type":0}]]],` +
			`"shared":[],"root":null}`}, expected{"gent: invalid type reference"}},
		{"duplicate field", input{`{"types":[],"layouts":[[["data"],["data"]]],` +
			`"shared":[],"root":null}`}, expected{"gent: duplicate layout field"}},
		{"duplicate type", input{`{"types":["bytes","bytes"],"layouts":[],"shared":[],"root":null}`},
			expected{"gent: invalid type table"}},
		{"unknown field", input{`{"types":[],"layouts":[],"shared":[],"root":null,"extra":1}`},
			expected{`gent: unknown saved document field "extra"`}},
		{"duplicate root", input{`{"types":[],"layouts":[],"shared":[],"root":null,"root":1}`},
			expected{`gent: duplicate saved document field "root"`}},
		{"untagged object", input{`{"types":[],"layouts":[],"shared":[],"root":{}}`},
			expected{"gent: untagged document object"}},
		{"unknown tag", input{`{"types":[],"layouts":[],"shared":[],"root":[-4]}`},
			expected{"gent: invalid layout reference"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			value := any("unchanged")
			err := NewCodecRegistry().Unmarshal(testDocument(t, test.input.payload), &value)
			assert.EqualError(t, err, test.expected.errorText)
			assert.Equal(t, "unchanged", value)
		})
	}
}

func TestSavedDocumentExpansionBounds(t *testing.T) {
	type input struct {
		limits CodecLimits
		depth  int
	}
	type expected struct{ errorText string }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"bytes", input{CodecLimits{MaxExpandedBytes: 64}, 8},
			expected{"gent: expanded value exceeds 64 bytes"}},
		{"values", input{CodecLimits{MaxExpandedValues: 64}, 8},
			expected{"gent: expanded value exceeds 64 values"}},
		{"default bound", input{CodecLimits{}, 63},
			expected{"gent: expanded value exceeds 4194304 values"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			shared := []string{`"saved"`}
			for id := 0; id < test.input.depth; id++ {
				shared = append(shared, fmt.Sprintf(`[-1,[-2,%d],[-2,%d]]`, id, id))
			}
			payload := fmt.Sprintf(`{"types":[],"layouts":[],"shared":[%s],"root":[-2,%d]}`,
				strings.Join(shared, ","), len(shared)-1)
			registry := NewCodecRegistry(WithCodecLimits(test.input.limits))
			var value any
			assert.EqualError(t, registry.Unmarshal(testDocument(t, payload), &value),
				test.expected.errorText)
			assert.Nil(t, value)
		})
	}
}

func TestSavedDocumentIntegrityAndTruncation(t *testing.T) {
	type input struct{ change func([]byte) []byte }
	type expected struct{ errorText string }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"type", input{func(data []byte) []byte {
			return bytes.Replace(data, []byte("/uint64"), []byte("/uint32"), 1)
		}}, expected{"gent: saved document digest mismatch"}},
		{"layout", input{func(data []byte) []byte {
			return bytes.Replace(data, []byte(`"constant":1`), []byte(`"constant":2`), 1)
		}}, expected{"gent: saved document digest mismatch"}},
		{"root", input{func(data []byte) []byte {
			return bytes.Replace(data, []byte(`95`), []byte(`96`), 1)
		}}, expected{"gent: saved document digest mismatch"}},
		{"version", input{func(data []byte) []byte {
			return bytes.Replace(data, []byte(`"formatVersion":1`), []byte(`"formatVersion":2`), 1)
		}}, expected{"gent: unsupported saved document version 2"}},
		{"truncated", input{func(data []byte) []byte { return data[:len(data)-1] }},
			expected{"gent: invalid encoded JSON"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			registry := NewCodecRegistry()
			data, err := registry.Marshal(uint64(95))
			assert.NoError(t, err)
			var value uint64
			assert.EqualError(t, registry.Unmarshal(test.input.change(data), &value),
				test.expected.errorText)
			assert.Equal(t, uint64(0), value)
		})
	}
}
