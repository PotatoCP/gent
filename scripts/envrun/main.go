// Command envrun loads provider test settings as data, without executing .env shell expressions.
// Fixture suites receive no live-provider settings. Live suites receive one selected provider.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/joho/godotenv"
)

var providers = []string{"OpenAI", "Anthropic", "Gemini", "VertexAI", "xAI", "DeepSeek",
	"OpenRouter", "vLLM", "Ollama", "llama.cpp", "SGLang"}

func providerPrefix(provider string) string {
	return "GENT_TEST_" + strings.ReplaceAll(strings.ToUpper(provider), ".", "") + "_"
}

func configured(values map[string]string, provider string) bool {
	prefix := providerPrefix(provider)
	switch provider {
	case "VertexAI":
		return values["GOOGLE_CLOUD_PROJECT"] != ""
	case "OpenRouter":
		return values[prefix+"KEY"] != "" && values[prefix+"MODEL"] != ""
	case "vLLM", "Ollama", "llama.cpp", "SGLang":
		return values[prefix+"URL"] != "" && values[prefix+"MODEL"] != ""
	default:
		return values[prefix+"KEY"] != ""
	}
}

func providerSetting(key string) bool {
	return strings.HasPrefix(key, "GENT_TEST_") || strings.HasPrefix(key, "GENT_LIVE_") ||
		key == "GOOGLE_APPLICATION_CREDENTIALS" || key == "GOOGLE_CLOUD_PROJECT" ||
		key == "GOOGLE_CLOUD_LOCATION"
}

func environment(file, provider string, inherited []string) ([]string, []string, error) {
	values, err := godotenv.Read(file)
	if os.IsNotExist(err) {
		values = map[string]string{}
	} else if err != nil {
		// A dotenv parse error can contain a credential line. Do not print its raw text.
		return nil, nil, fmt.Errorf("cannot parse provider settings in %s", file)
	}
	result := make([]string, 0, len(inherited))
	for _, entry := range inherited {
		key, value, _ := strings.Cut(entry, "=")
		values[key] = value
		if !providerSetting(key) {
			result = append(result, entry)
		}
	}
	var available []string
	for _, name := range providers {
		if configured(values, name) {
			available = append(available, name)
		}
	}
	if provider == "" {
		return result, available, nil
	}
	if !slices.Contains(available, provider) {
		return nil, available, fmt.Errorf("live provider %s has no complete configuration", provider)
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		if !strings.HasPrefix(key, providerPrefix(provider)) && key != "GENT_LIVE_REQUIRE_ALL" &&
			!(provider == "VertexAI" && strings.HasPrefix(key, "GOOGLE_") && providerSetting(key)) {
			continue
		}
		value := values[key]
		if key == "GOOGLE_APPLICATION_CREDENTIALS" && value != "" && !filepath.IsAbs(value) {
			value, err = filepath.Abs(filepath.Join(filepath.Dir(file), value))
			if err != nil {
				return nil, available, err
			}
		}
		result = append(result, key+"="+value)
	}
	return result, available, nil
}

func main() {
	file := flag.String("env-file", ".env", "Provider settings file; existing environment wins")
	provider := flag.String("provider", "", "One live provider; empty selects fixture-only settings")
	list := flag.Bool("list", false, "List configured live providers without revealing credentials")
	flag.Parse()
	env, available, err := environment(*file, *provider, os.Environ())
	if err != nil {
		log.Fatal(err)
	}
	if *list {
		for _, name := range providers {
			if slices.Contains(available, name) {
				fmt.Println(name)
			} else {
				fmt.Fprintf(os.Stderr, "%s: no complete live configuration\n", name)
			}
		}
		return
	}
	if flag.NArg() == 0 {
		log.Fatal("supply a command after --")
	}
	command := exec.Command(flag.Arg(0), flag.Args()[1:]...)
	command.Env = env
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := command.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			os.Exit(exit.ExitCode())
		}
		log.Fatal(err)
	}
}
