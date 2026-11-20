package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestProviderEnvironment(t *testing.T) {
	type input struct {
		provider  string
		inherited []string
	}
	type expected struct {
		env       []string
		available []string
		err       string
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	available := []string{"OpenAI", "Anthropic"}
	cases := []testCase{
		{"fixtures", input{"", []string{"PATH=/bin", "GENT_TEST_XAI_KEY=old"}},
			expected{[]string{"PATH=/bin"}, []string{"OpenAI", "Anthropic", "xAI"}, ""}},
		{"one provider", input{"OpenAI", []string{"PATH=/bin"}}, expected{[]string{
			"PATH=/bin", "GENT_LIVE_REQUIRE_ALL=0", "GENT_TEST_OPENAI_KEY=file-key",
			"GENT_TEST_OPENAI_MODEL=$(never-execute)",
		}, available, ""}},
		{"environment wins", input{"OpenAI", []string{"GENT_TEST_OPENAI_KEY=env-key"}},
			expected{[]string{"GENT_LIVE_REQUIRE_ALL=0", "GENT_TEST_OPENAI_KEY=env-key",
				"GENT_TEST_OPENAI_MODEL=$(never-execute)"}, available, ""}},
		{"incomplete local", input{provider: "Ollama"},
			expected{available: available, err: "live provider Ollama has no complete configuration"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), ".env")
			assert.NoError(t, os.WriteFile(file, []byte(`GENT_TEST_OPENAI_KEY=file-key
GENT_TEST_OPENAI_MODEL='$(never-execute)'
GENT_TEST_ANTHROPIC_KEY=other-key
GENT_TEST_OPENROUTER_KEY=router-key-without-model
GENT_TEST_OLLAMA_URL=http://localhost:11434
GENT_LIVE_REQUIRE_ALL=0
`), 0600))
			env, available, err := environment(file, test.input.provider, test.input.inherited)
			actual := expected{env: env, available: available}
			if err != nil {
				actual.err = err.Error()
			}
			assert.Equal(t, test.expected, actual)
		})
	}
}
