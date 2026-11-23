package gent

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type savedMapKey struct{ Value *int }

type savedMapEntry struct {
	Key   int
	Value string
}

func savedMapEntries(equalKeys, equalValues bool) []savedMapEntry {
	entries := make([]savedMapEntry, 32)
	for i := range entries {
		key, value := i, i
		if equalKeys {
			key = 7
		}
		if equalValues {
			value = 0
		}
		entries[i] = savedMapEntry{Key: key,
			Value: strings.Repeat("Keep the complete accepted value. ", 32) +
				fmt.Sprintf("value-%02d", value)}
	}
	return entries
}

func savedPointerMap(entries []savedMapEntry) map[*int]string {
	value := make(map[*int]string, len(entries))
	for _, entry := range entries {
		key := new(int)
		*key = entry.Key
		value[key] = entry.Value
	}
	return value
}

// Compare each logical entry independently of the codec. A wire-only comparison would miss an
// encoder that dropped equal entries in both the first write and the round trip.
func inspectSavedMap(t *testing.T, value any) []savedMapEntry {
	t.Helper()
	var entries []savedMapEntry
	iter := reflect.ValueOf(value).MapRange()
	for iter.Next() {
		key := iter.Key()
		for key.Kind() != reflect.Int {
			switch key.Kind() {
			case reflect.Pointer, reflect.Interface:
				key = key.Elem()
			case reflect.Struct:
				key = key.Field(0)
			case reflect.Array:
				key = key.Index(0)
			default:
				t.Fatalf("unsupported test key %s", key.Type())
			}
		}
		entries = append(entries, savedMapEntry{Key: int(key.Int()), Value: iter.Value().String()})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Key != entries[j].Key {
			return entries[i].Key < entries[j].Key
		}
		return entries[i].Value < entries[j].Value
	})
	return entries
}

func mapOrderBatch(value any) *SaveBatch {
	return &SaveBatch{SchemaVersion: RunSchemaVersion, RunId: "map-order",
		Events: []*RecordedEvent{{RunId: "map-order", EventSequenceNumber: 1, Type: "app:map",
			Ts: time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC), Payload: value}}}
}

func TestSavedMapOrderWithEqualKeys(t *testing.T) {
	type input struct{ value any }
	type expected struct {
		entries []savedMapEntry
		err     error
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	entries := savedMapEntries(true, false)
	pointers := savedPointerMap(entries)
	structs, arrays := make(map[savedMapKey]string), make(map[[1]*int]string)
	interfaces := make(map[any]string)
	for key, value := range pointers {
		structs[savedMapKey{Value: key}] = value
		arrays[[1]*int{key}] = value
		interfaces[key] = value
	}
	identical := savedMapEntries(true, true)
	unique := savedMapEntries(false, false)
	cases := []testCase{
		{"pointer", input{pointers}, expected{entries: entries}},
		{"struct", input{structs}, expected{entries: entries}},
		{"array", input{arrays}, expected{entries: entries}},
		{"interface", input{interfaces}, expected{entries: entries}},
		{"identical entries", input{savedPointerMap(identical)}, expected{entries: identical}},
		{"distinct saved keys", input{savedPointerMap(unique)}, expected{entries: unique}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			for _, compression := range []CodecCompression{CodecCompressionNone, CodecCompressionSnappy} {
				t.Run(string(compression), func(t *testing.T) {
					codecs := NewCodecRegistry(WithCodecCompression(compression))
					assert.NoError(t, codecs.RegisterType("app.map-key", 1, savedMapKey{}))
					encoded, err := codecs.Encode(test.input.value)
					if !assert.NoError(t, err) {
						return
					}
					stored, err := codecs.Marshal(test.input.value)
					if !assert.NoError(t, err) {
						return
					}
					digest, err := BatchDigest(mapOrderBatch(test.input.value), codecs)
					assert.NoError(t, err)
					for attempt := 0; attempt < 16; attempt++ {
						again, err := codecs.Encode(test.input.value)
						assert.NoError(t, err)
						assert.Equal(t, encoded, again)
						bytes, err := codecs.Marshal(test.input.value)
						assert.NoError(t, err)
						assert.Equal(t, stored, bytes)
						id, err := BatchDigest(mapOrderBatch(test.input.value), codecs)
						assert.NoError(t, err)
						assert.Equal(t, digest, id)
					}
					component, err := codecs.Decode(encoded)
					if !assert.NoError(t, err) {
						return
					}
					var document any
					if !assert.NoError(t, codecs.Unmarshal(stored, &document)) {
						return
					}
					for _, restored := range []any{component, document} {
						assert.Equal(t, reflect.TypeOf(test.input.value), reflect.TypeOf(restored))
						assert.Equal(t, test.expected, expected{entries: inspectSavedMap(t, restored)})
						again, err := codecs.Encode(restored)
						assert.NoError(t, err)
						assert.Equal(t, encoded, again)
						id, err := BatchDigest(mapOrderBatch(restored), codecs)
						assert.NoError(t, err)
						assert.Equal(t, digest, id)
					}
				})
			}
		})
	}
}

func TestSavedMapOrderKeepsOrdinaryReceipts(t *testing.T) {
	type input struct{ value any }
	type expected struct {
		digest string
		err    error
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	first, second := 1, 2
	// These receipts were captured with the original key-only comparator. Adding a value
	// tie-breaker must not change receipts when every saved key already has a distinct order.
	for _, test := range []testCase{
		{"strings", input{map[string]int{"a": 1, "b": 2}}, expected{
			digest: "70cc6d3abc659a4eff35c3f80edddc04184ffcdad5c6a2b78abfa4bed5a54d9b"}},
		{"distinct pointers", input{map[*int]string{&first: "a", &second: "b"}}, expected{
			digest: "dde37168c454589d5be2c4ec4d3f9a57e17bec5da0a6fbfcd6bfc32fc6e20f93"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			digest, err := BatchDigest(mapOrderBatch(test.input.value), NewCodecRegistry())
			assert.Equal(t, test.expected, expected{digest: digest, err: err})
		})
	}
}
