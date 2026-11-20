package main

import (
	"archive/zip"
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInspectRelease(t *testing.T) {
	type input struct{ files []string }
	type expected struct {
		count int
		err   string
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"library", input{[]string{"go.mod", "model.go", "LICENSE"}}, expected{3, ""}},
		{"missing code", input{[]string{"go.mod", "LICENSE"}},
			expected{err: "required file missing from release: model.go"}},
	}
	for _, name := range []string{"docs/build.mjs", "examples/history/main.go",
		"examples/chat/web/app.js", ".env", ".secrets/key.json", "node_modules/file.js"} {
		cases = append(cases, testCase{name, input{[]string{"go.mod", "model.go", "LICENSE", name}},
			expected{err: "excluded source or secret in release: " + name}})
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var data bytes.Buffer
			writer := zip.NewWriter(&data)
			for _, name := range test.input.files {
				_, err := writer.Create("gent/" + name)
				assert.NoError(t, err)
			}
			assert.NoError(t, writer.Close())
			count, err := inspect(data.Bytes(), "gent/")
			actual := expected{count: count}
			if err != nil {
				actual.err = err.Error()
			}
			assert.Equal(t, test.expected, actual)
		})
	}
}
