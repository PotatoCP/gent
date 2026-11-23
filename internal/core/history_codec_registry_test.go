package gent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCodecRegistriesKeepLocalBindings(t *testing.T) {
	type input struct {
		version int
		value   any
	}
	type expected struct{ value any }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	type first struct{ Text string }
	type second struct{ Count int }
	cases := []testCase{
		{"first binding", input{1, first{"saved"}}, expected{first{"saved"}}},
		{"second binding", input{2, second{5}}, expected{second{5}}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			registry := NewCodecRegistry()
			assert.NoError(t, registry.RegisterType("test:local", test.input.version, test.input.value))
			data, err := registry.Marshal(test.input.value)
			assert.NoError(t, err)
			var value any
			assert.NoError(t, registry.Unmarshal(data, &value))
			assert.Equal(t, test.expected, expected{value})
		})
	}
}

func TestDecodedSharedStringsOwnTheirInput(t *testing.T) {
	type label string
	registry := NewCodecRegistry()
	assert.NoError(t, registry.RegisterType("test:label", 1, label("")))
	text := strings.Repeat("<&> original input ", 4096)
	original := []any{text, text, label(text), json.Number("18446744073709551615")}
	data, err := registry.Marshal(original)
	assert.NoError(t, err)
	var restored []any
	assert.NoError(t, registry.Unmarshal(data, &restored))
	clear(data)
	assert.Equal(t, original, restored)
	restored[0] = "changed"
	assert.Equal(t, original[1:], restored[1:])
}
