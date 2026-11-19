package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/models"
	"github.com/stretchr/testify/assert"
)

func clearProviderEnvironment(t *testing.T) {
	t.Helper()
	for _, provider := range []string{"OPENAI", "ANTHROPIC", "GEMINI", "XAI"} {
		for _, suffix := range []string{"KEY", "MODEL", "URL"} {
			key := "GENT_TEST_" + provider + "_" + suffix
			// Setenv records the original setting for cleanup. Absence permits the file value.
			t.Setenv(key, "")
			assert.NoError(t, os.Unsetenv(key))
		}
	}
}

func TestPrepareProvider(t *testing.T) {
	type input struct {
		file, module, nestedFile, requested, choice string
		omitFile                                    bool
		exported                                    map[string]string
	}
	type expected struct {
		provider, output, error, modelType string
		config                             *models.Config
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	const configured = `# Credentials stay on the server.
export GENT_TEST_OPENAI_KEY="file-openai"
GENT_TEST_OPENAI_MODEL='test-openai'
GENT_TEST_OPENAI_URL="http://127.0.0.1:9000/v1" # Custom endpoint.
GENT_TEST_ANTHROPIC_KEY=file-anthropic
`
	const twoChoices = `Choose provider credentials:
  1. OpenAI
  2. Anthropic
Provider number or name: `
	const oneChoice = `Choose provider credentials:
  1. OpenAI
Provider number or name: `
	openAI := &models.Config{APIKey: "file-openai", Model: "test-openai",
		BaseURL: "http://127.0.0.1:9000/v1"}
	for _, test := range []testCase{
		{
			name: "select a configured provider by number",
			input: input{file: configured, choice: `2
`},
			expected: expected{provider: "anthropic", output: twoChoices,
				config: &models.Config{APIKey: "file-anthropic",
					Model: gent.ModelAnthropicClaude46Sonnet}, modelType: "*models.Anthropic"},
		},
		{
			name: "select by name and preserve explicit environment settings",
			input: input{file: configured, choice: ` OPENAI
`, exported: map[string]string{
				"GENT_TEST_OPENAI_KEY": "exported-key", "GENT_TEST_OPENAI_MODEL": "exported-model",
				"GENT_TEST_OPENAI_URL": "http://127.0.0.1:9001/v1",
			}},
			expected: expected{provider: "openai", output: twoChoices,
				config: &models.Config{APIKey: "exported-key", Model: "exported-model",
					BaseURL: "http://127.0.0.1:9001/v1"}, modelType: "*models.Responses"},
		},
		{
			name: "invalid choices do not select a provider",
			input: input{file: configured, choice: `
5
demo
1
`},
			expected: expected{provider: "openai", config: openAI, modelType: "*models.Responses",
				output: `Choose provider credentials:
  1. OpenAI
  2. Anthropic
Provider number or name: Choose a listed provider.
Provider number or name: Choose a listed provider.
Provider number or name: Choose a listed provider.
Provider number or name: `},
		},
		{
			name:  "one configured provider still requires a choice",
			input: input{file: "GENT_TEST_OPENAI_KEY=one-key", choice: "openai"},
			expected: expected{provider: "openai", output: oneChoice,
				config:    &models.Config{APIKey: "one-key", Model: gent.ModelOpenAIGPT54Mini},
				modelType: "*models.Responses"},
		},
		{
			name:  "closed input does not select the only provider",
			input: input{file: "GENT_TEST_OPENAI_KEY=one-key"},
			expected: expected{output: oneChoice,
				error: "no provider selected; use -provider <name> without a terminal"},
		},
		{
			name:  "explicit provider does not read stdin",
			input: input{file: configured, requested: "openai"},
			expected: expected{provider: "openai", config: openAI,
				modelType: "*models.Responses"},
		},
		{
			name:     "explicit demo does not parse credentials",
			input:    input{file: `GENT_TEST_OPENAI_KEY="unterminated`, requested: "demo"},
			expected: expected{provider: "demo", modelType: "*main.demoModel"},
		},
		{
			name:  "no configured credentials",
			input: input{file: "GENT_TEST_GEMINI_MODEL=test-model"},
			expected: expected{error: "no provider credentials found; " +
				"set a key in the project .env or use -provider demo"},
		},
		{
			name: "empty exported key disables file credentials",
			input: input{file: "GENT_TEST_OPENAI_KEY=file-key",
				exported: map[string]string{"GENT_TEST_OPENAI_KEY": ""}},
			expected: expected{error: "no provider credentials found; " +
				"set a key in the project .env or use -provider demo"},
		},
		{
			name: "missing file permits exported credentials",
			input: input{omitFile: true, requested: "gemini",
				exported: map[string]string{"GENT_TEST_GEMINI_KEY": "exported-gemini"}},
			expected: expected{provider: "gemini", modelType: "*models.Gemini",
				config: &models.Config{APIKey: "exported-gemini",
					Model: gent.ModelGoogleGemini25Flash}},
		},
		{
			name: "only the Gent project file supplies credentials",
			input: input{file: configured, nestedFile: "GENT_TEST_OPENAI_KEY=wrong-key",
				requested: "openai"},
			expected: expected{provider: "openai", config: openAI, modelType: "*models.Responses"},
		},
		{
			name: "unrelated project file is not loaded",
			input: input{file: configured, module: "example.com/other", requested: "xai",
				exported: map[string]string{"GENT_TEST_XAI_KEY": "exported-xai"}},
			expected: expected{provider: "xai", modelType: "*models.Responses",
				config: &models.Config{APIKey: "exported-xai", Model: gent.ModelXAIGrok43}},
		},
		{
			name:     "malformed file does not disclose its text",
			input:    input{file: `GENT_TEST_OPENAI_KEY="private-value`},
			expected: expected{error: "parse PROJECT_ENV: invalid .env syntax"},
		},
		{
			name: "dotenv values are not executed as shell commands",
			input: input{requested: "openai", file: `GENT_TEST_OPENAI_KEY=file-key
GENT_TEST_OPENAI_MODEL='$(printf private-value)'
`},
			expected: expected{provider: "openai", modelType: "*models.Responses",
				config: &models.Config{APIKey: "file-key", Model: "$(printf private-value)"}},
		},
		{
			name:     "unknown explicit provider",
			input:    input{file: configured, requested: "other"},
			expected: expected{error: `unknown provider "other"`},
		},
		{
			name:     "explicit provider requires its own credentials",
			input:    input{file: configured, requested: "gemini"},
			expected: expected{error: "set GENT_TEST_GEMINI_KEY in the project .env or environment"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			clearProviderEnvironment(t)
			for name, value := range test.input.exported {
				t.Setenv(name, value)
			}
			root := t.TempDir()
			directory := filepath.Join(root, "examples", "chat")
			assert.NoError(t, os.MkdirAll(directory, 0700))
			module := test.input.module
			if module == "" {
				module = "github.com/rickchristie/gent"
			}
			assert.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"),
				[]byte("module "+module), 0600))
			assert.NoError(t, os.WriteFile(filepath.Join(directory, "go.mod"),
				[]byte("module example.com/gent-chat"), 0600))
			if !test.input.omitFile {
				assert.NoError(t, os.WriteFile(filepath.Join(root, ".env"),
					[]byte(test.input.file), 0600))
			}
			if test.input.nestedFile != "" {
				assert.NoError(t, os.WriteFile(filepath.Join(directory, ".env"),
					[]byte(test.input.nestedFile), 0600))
			}
			var output bytes.Buffer
			selected, environment, err := prepareProvider(directory, test.input.requested,
				strings.NewReader(test.input.choice), &output)
			actual := expected{provider: selected, output: output.String()}
			if err != nil {
				actual.error = err.Error()
			} else {
				factory, factoryErr := providerModel(selected, 0, environment)
				if assert.NoError(t, factoryErr) {
					actual.modelType = fmt.Sprintf("%T", factory())
				}
				if selected != "demo" {
					actual.config, err = providerConfig(selected, environment)
					assert.NoError(t, err)
				}
			}
			want := test.expected
			want.error = strings.ReplaceAll(want.error, "PROJECT_ENV", filepath.Join(root, ".env"))
			assert.Equal(t, want, actual)
			// Reading the file must not export its credentials into the process environment.
			_, overridden := test.input.exported["GENT_TEST_OPENAI_KEY"]
			if !overridden {
				value, exists := os.LookupEnv("GENT_TEST_OPENAI_KEY")
				assert.Equal(t, "", value)
				assert.False(t, exists)
			}
		})
	}
}

type failedProviderIO struct{}

func (failedProviderIO) Read([]byte) (int, error)  { return 0, io.ErrUnexpectedEOF }
func (failedProviderIO) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestProviderSelectionIOErrors(t *testing.T) {
	type input struct {
		environment map[string]string
	}
	type mocks struct {
		input  io.Reader
		output io.Writer
	}
	type expected struct {
		provider, error string
		cause           error
	}
	type testCase struct {
		name     string
		input    input
		mocks    mocks
		expected expected
	}
	for _, test := range []testCase{
		{
			name:  "input failure",
			input: input{environment: map[string]string{"GENT_TEST_OPENAI_KEY": "test-key"}},
			mocks: mocks{input: failedProviderIO{}, output: io.Discard},
			expected: expected{error: "read provider selection: unexpected EOF",
				cause: io.ErrUnexpectedEOF},
		},
		{
			name:  "output failure",
			input: input{environment: map[string]string{"GENT_TEST_OPENAI_KEY": "test-key"}},
			mocks: mocks{input: strings.NewReader("1"), output: failedProviderIO{}},
			expected: expected{error: "write provider menu: io: read/write on closed pipe",
				cause: io.ErrClosedPipe},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			selected, err := chooseProvider(test.mocks.input, test.mocks.output,
				test.input.environment)
			if !assert.Error(t, err) {
				return
			}
			actual := expected{provider: selected, error: err.Error(), cause: test.expected.cause}
			assert.Equal(t, test.expected, actual)
			assert.True(t, errors.Is(err, test.expected.cause))
		})
	}
}
