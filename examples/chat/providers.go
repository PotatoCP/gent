package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/models"
	"golang.org/x/mod/modfile"
)

type providerOption struct {
	name, label, defaultModel string
}

var providerOptions = []providerOption{
	{"openai", "OpenAI", gent.ModelOpenAIGPT54Mini},
	{"anthropic", "Anthropic", gent.ModelAnthropicClaude46Sonnet},
	{"gemini", "Gemini", gent.ModelGoogleGemini25Flash},
	{"xai", "xAI", gent.ModelXAIGrok43},
}

// prepareProvider resolves credentials before creating workers. Demo and explicit provider flags
// do not read stdin, so scripts and browser tests do not depend on an interactive terminal.
func prepareProvider(directory, requested string, input io.Reader, output io.Writer) (
	string, map[string]string, error,
) {
	if requested == "demo" {
		return requested, nil, nil
	}
	environment, err := loadProviderEnvironment(directory)
	if err != nil {
		return "", nil, err
	}
	if requested != "" {
		if _, err := providerConfig(requested, environment); err != nil {
			return "", nil, err
		}
		return requested, environment, nil
	}
	selected, err := chooseProvider(input, output, environment)
	if err != nil {
		return "", nil, err
	}
	return selected, environment, nil
}

func loadProviderEnvironment(directory string) (map[string]string, error) {
	environment := make(map[string]string)
	for {
		module, err := os.ReadFile(filepath.Join(directory, "go.mod"))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("find project settings: %w", err)
		}
		// The example has its own module. Use Gent's root, not the first .env found on the path.
		if modfile.ModulePath(module) == "github.com/rickchristie/gent" {
			path := filepath.Join(directory, ".env")
			data, err := os.ReadFile(path)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("read project settings: %w", err)
			}
			if err == nil {
				environment, err = godotenv.Unmarshal(string(data))
				if err != nil {
					// Parser diagnostics can include credential text. Report the failure safely.
					return nil, fmt.Errorf("parse %s: invalid .env syntax", path)
				}
			}
			break
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			break
		}
		directory = parent
	}
	// Explicit environment values take precedence, including an empty value that disables a key.
	// Keep the merged settings local; loading this example must not change the process environment.
	for _, provider := range providerOptions {
		for _, suffix := range []string{"KEY", "MODEL", "URL"} {
			name := "GENT_TEST_" + strings.ToUpper(provider.name) + "_" + suffix
			if value, exists := os.LookupEnv(name); exists {
				environment[name] = value
			}
		}
	}
	return environment, nil
}

func chooseProvider(
	input io.Reader, output io.Writer, environment map[string]string,
) (string, error) {
	var available []providerOption
	menu := "Choose provider credentials:\n"
	for _, provider := range providerOptions {
		key := "GENT_TEST_" + strings.ToUpper(provider.name) + "_KEY"
		if strings.TrimSpace(environment[key]) == "" {
			continue
		}
		available = append(available, provider)
		menu += fmt.Sprintf("  %d. %s\n", len(available), provider.label)
	}
	if len(available) == 0 {
		return "", errors.New(
			"no provider credentials found; set a key in the project .env or use -provider demo",
		)
	}
	if _, err := io.WriteString(output, menu); err != nil {
		return "", fmt.Errorf("write provider menu: %w", err)
	}
	scanner := bufio.NewScanner(input)
	for {
		if _, err := io.WriteString(output, "Provider number or name: "); err != nil {
			return "", fmt.Errorf("write provider prompt: %w", err)
		}
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return "", fmt.Errorf("read provider selection: %w", err)
			}
			return "", errors.New("no provider selected; use -provider <name> without a terminal")
		}
		choice := strings.ToLower(strings.TrimSpace(scanner.Text()))
		for i, provider := range available {
			if choice == provider.name || choice == strconv.Itoa(i+1) {
				return provider.name, nil
			}
		}
		if _, err := io.WriteString(output, "Choose a listed provider.\n"); err != nil {
			return "", fmt.Errorf("write provider selection help: %w", err)
		}
	}
}

func providerConfig(provider string, environment map[string]string) (*models.Config, error) {
	for _, option := range providerOptions {
		if option.name != provider {
			continue
		}
		prefix := "GENT_TEST_" + strings.ToUpper(provider)
		key := environment[prefix+"_KEY"]
		if strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("set %s_KEY in the project .env or environment", prefix)
		}
		model := environment[prefix+"_MODEL"]
		if model == "" {
			model = option.defaultModel
		}
		return &models.Config{
			APIKey: key, Model: model, BaseURL: environment[prefix+"_URL"],
		}, nil
	}
	return nil, fmt.Errorf("unknown provider %q", provider)
}

func providerModel(provider string, delay time.Duration, environment map[string]string) (
	func() gent.Model, error,
) {
	if provider == "demo" {
		return func() gent.Model { return &demoModel{delay: delay} }, nil
	}
	config, err := providerConfig(provider, environment)
	if err != nil {
		return nil, err
	}
	switch provider {
	case "openai", "xai":
		baseURL := config.BaseURL
		if baseURL == "" {
			baseURL = "https://api.openai.com/v1"
			if provider == "xai" {
				baseURL = "https://api.x.ai/v1"
			}
		}
		return func() gent.Model {
			return models.NewResponses(&models.ResponsesConfig{Provider: provider,
				BaseURL: baseURL, APIKey: config.APIKey, Model: config.Model})
		}, nil
	case "anthropic":
		return func() gent.Model { return models.NewAnthropic(config) }, nil
	case "gemini":
		return func() gent.Model { return models.NewGemini(config) }, nil
	default:
		return nil, fmt.Errorf("unknown provider %q", provider)
	}
}
