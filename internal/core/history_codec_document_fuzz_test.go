package gent

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func FuzzSavedDocument(f *testing.F) {
	for _, payload := range []string{
		oversizedArrayDocument,
		`{"types":["nil"],"layouts":[[["type",{"type":0}],["version",{"constant":1}],` +
			`["data"]]],"shared":[],"root":[0,null]}`,
		`{"types":[],"layouts":[],"shared":[[-2,0]],"root":[-2,0]}`,
		`{"types":[],"layouts":[[["data"],["data"]]],"shared":[],"root":[0,null,null]}`,
		`{"types":[],"layouts":[],"shared":[null,[-1,[-2,0],[-2,0]]],"root":[-2,1]}`,
	} {
		f.Add([]byte(payload))
	}
	f.Fuzz(func(t *testing.T, payload []byte) {
		if len(payload) > 16*1024 {
			return
		}
		// Seal mutated bodies so the fuzzer reaches reference and layout validation as well as
		// the JSON parser. A storage digest alone is not an allocation or schema check.
		data, err := json.Marshal(savedDocument{FormatVersion: savedDocumentV3,
			Data: payload, Digest: v3DocumentDigest(payload)})
		if err != nil {
			return
		}
		var document savedDocument
		if err := json.Unmarshal(data, &document); err != nil {
			t.Fatal(err)
		}
		document.Digest = v3DocumentDigest(document.Data)
		data, err = json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		registry := NewCodecRegistry(WithCodecCompression(CodecCompressionNone),
			WithCodecLimits(CodecLimits{MaxEncodedBytes: 32 * 1024,
				MaxExpandedBytes: 128 * 1024, MaxExpandedValues: 8192, MaxBinaryBytes: 16 * 1024}))
		var value any
		if err := registry.Unmarshal(data, &value); err != nil {
			return
		}
		encoded, err := registry.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		size, err := registry.uncompressedSize(value)
		assert.NoError(t, err)
		assert.Equal(t, len(encoded), size)
		var restored any
		if err := registry.Unmarshal(encoded, &restored); err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, value, restored)
	})
}
