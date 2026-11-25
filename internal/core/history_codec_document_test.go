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

type savedDocument struct {
	FormatVersion int             `json:"formatVersion"`
	Data          json.RawMessage `json:"data"`
	Digest        string          `json:"digest"`
}

type savedDocumentData struct {
	Types   []string          `json:"types"`
	Layouts [][][]any         `json:"layouts"`
	Shared  []json.RawMessage `json:"shared"`
	Root    json.RawMessage   `json:"root"`
}

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
	document.Digest = v3DocumentDigest(document.Data)
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
		{"unsupported format", input{func(value *savedDocument) { value.FormatVersion = 4 }},
			expected{result: &RunStateLoadResult{Status: RunStateIncompatible,
				Detail: "gent: unsupported saved document version 4"}}},
		{"changed table", input{func(value *savedDocument) {
			value.Data = bytes.Replace(value.Data, []byte(`"run:document"`), []byte(`"run:changed"`), 1)
		}}, expected{result: &RunStateLoadResult{Status: RunStateInvalid,
			Detail: "gent: saved document digest mismatch"}}},
		{"invalid state with valid integrity", input{func(value *savedDocument) {
			value.Data = bytes.Replace(value.Data, []byte(`"run:document"`), []byte(`""`), 1)
			value.Digest = v3DocumentDigest(value.Data)
		}}, expected{result: &RunStateLoadResult{Status: RunStateInvalid,
			Detail: "gent: saved run ID is empty"}}},
		{"invalid reference with valid integrity", input{func(value *savedDocument) {
			value.Data = json.RawMessage(`{"types":[],"layouts":[],"shared":[],"root":[-2,0]}`)
			value.Digest = v3DocumentDigest(value.Data)
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
			var document savedDocument
			assert.NoError(t, json.Unmarshal(data, &document))
			test.input.change(&document)
			data, err = json.Marshal(document)
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

func TestRunStateChecksLogicalSchemaInsideStorageV3(t *testing.T) {
	type input struct{ value any }
	type expected struct{ result *RunStateLoadResult }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"wrong document kind", input{"text"}, expected{&RunStateLoadResult{Status: RunStateInvalid,
			Detail: "gent: RunStateSnapshot contains string, expected run state"}}},
		{"future run schema", input{runStateData{SchemaVersion: RunSchemaVersion + 1, RunId: "saved"}},
			expected{&RunStateLoadResult{Status: RunStateIncompatible,
				Detail: fmt.Sprintf("gent: unsupported run schema %d", RunSchemaVersion+1)}}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			codecs := NewCodecRegistry()
			data, err := codecs.Marshal(test.input.value)
			if !assert.NoError(t, err) {
				return
			}
			result, err := codecs.UnmarshalRunState(data)
			assert.NoError(t, err)
			assert.Equal(t, test.expected.result, result)
		})
	}
}

func TestRunStorageSharesVersionMarker(t *testing.T) {
	codecs := NewCodecRegistry()
	runID, batches := recordedStateFixture(t, 1, codecs, nil)
	state, view := NewRunState(runID), NewRunView(runID)
	for _, data := range batches {
		applyRecordedState(t, state, codecs, data)
		var batch SaveBatch
		if !assert.NoError(t, codecs.Unmarshal(data, &batch)) {
			return
		}
		result, err := view.Apply(&batch, codecs)
		if !assert.NoError(t, err) || !assert.Equal(t, &ApplyStateResult{Status: ApplyApplied}, result) {
			return
		}
	}
	archive, err := codecs.Marshal(view)
	if !assert.NoError(t, err) {
		return
	}
	type input struct{ data []byte }
	type expected struct{ version uint64 }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	for _, test := range []testCase{
		{"batch", input{batches[0]}, expected{3}},
		{"RunStateSnapshot", input{encodeRunState(t, codecs, state)}, expected{3}},
		{"archive", input{archive}, expected{3}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var header struct {
				FormatVersion uint64 `json:"formatVersion"`
			}
			assert.NoError(t, json.Unmarshal(test.input.data, &header))
			assert.Equal(t, test.expected, expected{header.FormatVersion})
		})
	}
}

// Future versions can replace every v3 field except the discriminator. The dispatcher must
// classify the version before consulting table, compression, or schema rules.
func TestSavedDocumentVersionDispatch(t *testing.T) {
	type input struct{ document string }
	type expected struct {
		status RunStateLoadStatus
		detail string
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	const badVersion = "gent: formatVersion must be a positive unsigned 64-bit integer"
	cases := []testCase{
		{"plain development format", input{`{"formatVersion":1,"data":{},"digest":""}`},
			expected{RunStateIncompatible, "gent: unsupported saved document version 1"}},
		{"compressed development format", input{`{"formatVersion":2,"data":"","digest":""}`},
			expected{RunStateIncompatible, "gent: unsupported saved document version 2"}},
		{"future payload and reordered header", input{`{"binary":"AA==","formatVersion":4}`},
			expected{RunStateIncompatible, "gent: unsupported saved document version 4"}},
		{"largest future version", input{`{"formatVersion":18446744073709551615}`},
			expected{RunStateIncompatible, "gent: unsupported saved document version 18446744073709551615"}},
		{"missing", input{`{"data":{},"digest":""}`}, expected{RunStateInvalid,
			`gent: missing saved document field "formatVersion"`}},
		{"duplicate", input{`{"formatVersion":4,"formatVersion":3}`}, expected{RunStateInvalid,
			`gent: duplicate saved document field "formatVersion"`}},
		{"zero", input{`{"formatVersion":0}`}, expected{RunStateInvalid, badVersion}},
		{"negative", input{`{"formatVersion":-1}`}, expected{RunStateInvalid, badVersion}},
		{"quoted", input{`{"formatVersion":"3"}`}, expected{RunStateInvalid, badVersion}},
		{"fraction", input{`{"formatVersion":3.5}`}, expected{RunStateInvalid, badVersion}},
		{"overflow", input{`{"formatVersion":18446744073709551616}`},
			expected{RunStateInvalid, badVersion}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			codecs := NewCodecRegistry()
			data := []byte(test.input.document)
			var target any = "unchanged"
			assert.EqualError(t, codecs.Unmarshal(data, &target), test.expected.detail)
			assert.Equal(t, "unchanged", target)
			loaded, err := codecs.UnmarshalRunState(data)
			assert.NoError(t, err)
			assert.Equal(t, &RunStateLoadResult{Status: test.expected.status,
				Detail: test.expected.detail}, loaded)
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
	data, err := json.Marshal(map[string]any{"schemaVersion": 7,
		"state": json.RawMessage(encoded), "digest": hex.EncodeToString(digest[:])})
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
	assert.Equal(t, v3DocumentDigest(document.Data), document.Digest)
	indexed, err := indexSavedJSON(document.Data)
	if !assert.NoError(t, err) {
		t.FailNow()
	}
	indexed, err = codecs.v3DocumentData(indexed)
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
	data, err := json.Marshal(savedDocument{FormatVersion: savedDocumentV3,
		Data: content, Digest: v3DocumentDigest(content)})
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
			return bytes.Replace(data, []byte(`"formatVersion":3`), []byte(`"formatVersion":4`), 1)
		}}, expected{"gent: unsupported saved document version 4"}},
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
