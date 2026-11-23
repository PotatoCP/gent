// Package testutil provides shared test infrastructure for integration
// test scenarios.
package testutil

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/agents/react"
	"github.com/rickchristie/gent/common"
	"github.com/rickchristie/gent/compaction"
	"github.com/rickchristie/gent/executor"
	"github.com/rickchristie/gent/format"
	"github.com/rickchristie/gent/models"
	"github.com/rickchristie/gent/search"
	"github.com/rickchristie/gent/toolchain"
)

// ToolChainType specifies which toolchain to use.
type ToolChainType string

const (
	// ToolChainRegistry selects the native tool registry for the integration run.
	ToolChainRegistry ToolChainType = "registry"
	// ToolChainSearch selects tool discovery through the search chain.
	ToolChainSearch ToolChainType = "search"
)

// CompactionType specifies the scratchpad context management strategy.
type CompactionType string

const (
	// CompactionNone disables compaction in the integration fixture.
	CompactionNone CompactionType = "none"
	// CompactionSlidingWindow keeps the configured recent history window.
	CompactionSlidingWindow CompactionType = "sliding_window"
	// CompactionSummarization summarizes earlier active history with the configured model.
	CompactionSummarization CompactionType = "summarization"
)

// CompactionConfig configures scratchpad context management.
type CompactionConfig struct {
	// Type selects the compaction strategy.
	Type CompactionType
	// TriggerIterations: compact every N iterations.
	TriggerIterations int64
	// WindowSize: for sliding window, how many recent iterations to keep.
	WindowSize int
	// KeepRecent: for summarization, how many recent iterations
	// to preserve.
	KeepRecent int
}

// TestConfig configures how integration test output is displayed.
type TestConfig struct {
	// ToolChain specifies which toolchain to use.
	ToolChain ToolChainType
	// SearchHintType controls how the tool summary is
	// shown in SearchChain prompts. Only used when
	// ToolChain is ToolChainSearch.
	SearchHintType toolchain.SearchHintType
	// PinTools lists tool names to pin in SearchChain.
	// Only used when ToolChain is ToolChainSearch.
	PinTools []string
	// WrapPTC wraps the toolchain with
	// JsToolChainWrapper for programmatic tool calling.
	WrapPTC bool
	// PTCCodeOnly disables direct tool calls in the PTC
	// wrapper, forcing all tool calls through code
	// execution. Only used when WrapPTC is true.
	PTCCodeOnly bool
	// ShowIterationHistory prints full iteration history at the end.
	ShowIterationHistory bool
	// ShowEvents prints all events at the end.
	ShowEvents bool
	// LogWriter is an optional writer for full debug logging.
	LogWriter io.Writer
	// Compaction configures scratchpad context management.
	Compaction CompactionConfig
	// Embedder for hybrid BM25+semantic search. Used by both tool search (SearchChain)
	// and policy search (PolicySearchTool). Created by DefaultTestConfig.
	Embedder search.Embedder
	// ModelName overrides the LLM model. An empty value uses [DefaultModelName].
	ModelName string
}

// DefaultTestConfig returns a config suitable for go test with native registry.
// Creates an ONNX embedder (multilingual-e5-small) for hybrid search. Panics if
// the model is not downloaded — run `gent setup onnx` first.
func DefaultTestConfig() TestConfig {
	return TestConfig{
		ToolChain:            ToolChainRegistry,
		ShowIterationHistory: true,
		ShowEvents:           true,
		Embedder:             createEmbedder(),
	}
}

// createEmbedder creates an ONNX embedder using multilingual-e5-small. This is a
// requirement for all integration tests — both tool search and policy search use
// hybrid BM25+semantic search.
func createEmbedder() search.Embedder {
	cfg := common.ConfigsForModel("multilingual-e5-small")[0]
	if !common.ModelDownloaded(&cfg.Model) {
		panic(
			"integration tests require multilingual-e5-small model. " +
				"Run: gent setup onnx && gent model download multilingual-e5-small",
		)
	}
	dir, err := common.ModelDir(cfg.Model.Name)
	if err != nil {
		panic("failed to get model dir: " + err.Error())
	}
	embedder, err := search.NewOnnxEmbedder(cfg, search.OnnxOptions{
		ModelPath:      filepath.Join(dir, cfg.Model.ModelFile),
		TokenizerPath:  filepath.Join(dir, "tokenizer.json"),
		NumThreads:     2,
		MaxConcurrency: 2,
	})
	if err != nil {
		panic("failed to create ONNX embedder: " + err.Error())
	}
	return embedder
}

// DefaultModelName is the default LLM model for integration tests.
const DefaultModelName = gent.ModelXAIGrok47

// ModelOption defines an LLM model available for integration tests.
type ModelOption struct {
	Label   string // provider label in test output
	Name    string // model ID for API calls
	EnvKey  string // env var for API key
	BaseURL string // API base URL (empty = OpenAI default)
}

// API base URLs for model providers.
const (
	baseURLXAI    = "https://api.x.ai/v1"
	baseURLGemini = "https://generativelanguage.googleapis.com/v1beta"
)

// Environment variable keys for API authentication.
const (
	envKeyXAI       = "GENT_TEST_XAI_KEY"
	envKeyOpenAI    = "GENT_TEST_OPENAI_KEY"
	envKeyGemini    = "GENT_TEST_GEMINI_KEY"
	envKeyAnthropic = "GENT_TEST_ANTHROPIC_KEY"
)

// AvailableModels supplies provider settings for scenario and native request tests.
// The server example uses its own configured model selection and project .env settings.
var AvailableModels = []ModelOption{
	{
		Label: "xAI grok-4.7", Name: gent.ModelXAIGrok47,
		EnvKey: envKeyXAI, BaseURL: baseURLXAI,
	},
	{
		Label: "OpenAI gpt-6-astra", Name: gent.ModelOpenAIGPT6Astra,
		EnvKey: envKeyOpenAI,
	},
	{
		Label: "Google gemini-4-argon", Name: gent.ModelGoogleGemini4Argon,
		EnvKey: envKeyGemini, BaseURL: baseURLGemini,
	},
	{
		Label: "Anthropic claude-fable-5-1", Name: gent.ModelAnthropicClaude51Fable,
		EnvKey: envKeyAnthropic,
	},
}

// CreateModel creates an LLM model for testing. If modelName is
// empty, uses [DefaultModelName]. Looks up the model in
// [AvailableModels] to determine the API key env var and base URL.
func CreateModel(
	modelName string,
) (gent.Model, error) {
	if modelName == "" {
		modelName = DefaultModelName
	}

	// Find model config.
	var opt ModelOption
	for _, m := range AvailableModels {
		if m.Name == modelName {
			opt = m
			break
		}
	}
	if opt.Name == "" {
		// Unknown model — assume xAI.
		opt = ModelOption{
			Name: modelName, EnvKey: envKeyXAI,
			BaseURL: baseURLXAI,
		}
	}

	apiKey := os.Getenv(opt.EnvKey)
	if apiKey == "" {
		return nil, fmt.Errorf(
			"%s environment variable not set", opt.EnvKey,
		)
	}

	httpClient := &http.Client{}

	if opt.EnvKey == envKeyOpenAI || opt.EnvKey == envKeyXAI {
		provider, baseURL := "openai", "https://api.openai.com/v1"
		if opt.EnvKey == envKeyXAI {
			provider, baseURL = "xai", baseURLXAI
		}
		return models.NewResponses(&models.ResponsesConfig{Provider: provider, BaseURL: baseURL,
			APIKey: apiKey, Model: modelName, HTTPClient: httpClient}), nil
	}

	config := &models.Config{APIKey: apiKey, Model: modelName, HTTPClient: httpClient}
	switch opt.EnvKey {
	case envKeyAnthropic:
		return models.NewAnthropic(config), nil
	case envKeyGemini:
		return models.NewGemini(config), nil
	default:
		return nil, fmt.Errorf("unsupported provider for model %q", modelName)
	}
}

// CreateToolChain creates the appropriate toolchain based on config. For
// ToolChainSearch, returns a SearchChain with hybrid BM25+semantic search.
// When WrapPTC is enabled, output schema printing is turned on.
func CreateToolChain(config TestConfig) gent.ToolChain {
	switch config.ToolChain {
	case ToolChainSearch:
		tc := toolchain.NewSearch(config.SearchHintType).
			RegisterEngine(
				toolchain.NewFusedToolSearcher(config.Embedder),
			)
		return tc
	default:
		return toolchain.New()
	}
}

// InitializeToolChain applies pinned tools and calls
// Initialize() on a SearchChain toolchain. No-op for
// other toolchain types.
func InitializeToolChain(
	tc gent.ToolChain,
	config TestConfig,
) error {
	if stc, ok := tc.(*toolchain.SearchChain); ok {
		for _, name := range config.PinTools {
			stc.Pin(name)
		}
		return stc.Initialize()
	}
	return nil
}

// WrapToolChain wraps the toolchain with
// JsToolChainWrapper if WrapPTC is enabled. Must be
// called after InitializeToolChain.
func WrapToolChain(
	tc gent.ToolChain,
	config TestConfig,
) gent.ToolChain {
	if config.WrapPTC {
		w := toolchain.NewJsToolChainWrapper(tc)
		if config.PTCCodeOnly {
			w.WithDirectCallDisabled()
		}
		return w
	}
	return tc
}

// ConfigureCompaction sets up compaction on the shared configuration
// based on the config. The model and textFormat are needed for summarization strategy.
func ConfigureCompaction(
	target *gent.Config, config CompactionConfig,
	model gent.Model, textFormat gent.TextFormat,
) {
	if config.Type == CompactionNone || config.Type == "" {
		return
	}

	trigger := compaction.NewStatThresholdTrigger().
		OnCounter(gent.SCIterations, config.TriggerIterations)

	var strategy gent.CompactionStrategy
	switch config.Type {
	case CompactionSlidingWindow:
		strategy = compaction.NewSlidingWindow(config.WindowSize)
	case CompactionSummarization:
		strategy = compaction.NewSummarization(model, textFormat).WithKeepRecent(config.KeepRecent)
	default:
		return
	}

	target.CompactionTrigger, target.CompactionStrategy = trigger, strategy
}

// PrintHeader prints a header line.
func PrintHeader(w io.Writer, title string) {
	line := strings.Repeat("=", 80)
	fmt.Fprintln(w, line)
	fmt.Fprintln(w, title)
	fmt.Fprintln(w, line)
}

// PrintSection prints a section header.
func PrintSection(w io.Writer, title string) {
	fmt.Fprintf(w, "--- %s ---\n", title)
}

// CriticalRules returns the standard critical rules for integration test
// scenarios. Extra rules are appended after the standard set. When the
// config uses SearchChain or PTC, a tool schema discovery rule is added.
func CriticalRules(
	config TestConfig, extra ...string,
) string {
	rules := `DO NOT HALLUCINATE
- ALWAYS search and read the relevant policy BEFORE taking any action
- Every claim in your answer MUST come from tool outputs or user-provided information
- NEVER invent specific data (IDs, prices, times, availability)
- If information is missing, say so explicitly`
	for _, r := range extra {
		rules += "\n- " + r
	}
	if config.ToolChain == ToolChainSearch {
		rules += "\n- You must have the tool schemas in " +
			"scratchpad before calling any tool. " +
			"Use tool_registry_search and get_tool_schema."
	}
	return rules
}

// PolicySuggestionHook adds policy guidance to the first three main model requests of a turn.
// The request hook keeps generated guidance separate from saved user input. It does not change
// accepted history. Create or reset this helper before each serial turn.
type PolicySuggestionHook struct {
	suggester  func(ctx context.Context, text string) string
	text       string
	suggestion string
	calls      int
}

func (h *PolicySuggestionHook) BeforeModelCall(
	ctx context.Context, event *gent.BeforeModelCallEvent,
) error {
	if event.StreamTopicId != llmResponseTopic || h.calls >= 3 {
		return nil
	}
	if h.calls == 0 {
		h.suggestion = h.suggester(ctx, h.text)
	}
	h.calls++
	if h.suggestion == "" {
		return nil
	}
	content := format.NewXML().FormatSections([]gent.FormattedSection{
		{Name: "policy_suggestion", Content: h.suggestion},
	})
	event.Request.Messages = append(event.Request.Messages,
		gent.TextParts(gent.ChatMessageTypeHuman, content))
	return nil
}

// ContainsIgnoreCase checks if s contains substr, case-insensitive.
func ContainsIgnoreCase(s, substr string) bool {
	sLower := make([]byte, len(s))
	substrLower := make([]byte, len(substr))
	for i := range len(s) {
		if s[i] >= 'A' && s[i] <= 'Z' {
			sLower[i] = s[i] + 32
		} else {
			sLower[i] = s[i]
		}
	}
	for i := range len(substr) {
		if substr[i] >= 'A' && substr[i] <= 'Z' {
			substrLower[i] = substr[i] + 32
		} else {
			substrLower[i] = substr[i]
		}
	}

	for i := 0; i <= len(sLower)-len(substrLower); i++ {
		match := true
		for j := range len(substrLower) {
			if sLower[i+j] != substrLower[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// -------------------------------------------------------------------------
// ScenarioConfig + RunScenario
// -------------------------------------------------------------------------

// ScenarioConfig defines parameters for a test scenario.
type ScenarioConfig struct {
	Name            string
	HeaderTitle     string
	CustomerRequest string
	MaxIterations   float64
	RegisterTools   func(tc gent.ToolChain)
	SystemPrompt    string
	CriticalRules   string
	// PolicySuggester generates a policy suggestion section for
	// the system prompt based on the conversation text. Called
	// once before the agent loop starts. Returns empty string
	// if no policies are relevant.
	PolicySuggester func(ctx context.Context, text string) string
}

// RunScenario executes a test scenario with the given configuration.
func RunScenario(
	ctx context.Context,
	w io.Writer,
	testCfg TestConfig,
	scenario ScenarioConfig,
) error {
	writeMu := &sync.Mutex{}
	w = &serialWriter{mu: writeMu, target: w}
	if testCfg.LogWriter != nil {
		testCfg.LogWriter = &serialWriter{mu: writeMu, target: testCfg.LogWriter}
	}
	model, err := CreateModel(testCfg.ModelName)
	if err != nil {
		return err
	}

	tc := CreateToolChain(testCfg)
	scenario.RegisterTools(tc)
	if err := InitializeToolChain(tc, testCfg); err != nil {
		return fmt.Errorf(
			"failed to initialize toolchain: %w", err,
		)
	}
	tc = WrapToolChain(tc, testCfg)

	seq, err := NewIntegrationTrace("")
	if err != nil {
		return err
	}
	config := &gent.Config{
		ToolChain:   tc,
		Subscribers: []*gent.SubscriberRegistration{{Subscriber: seq, Delivery: gent.SubscriberLive}},
		Limits: []gent.Limit{{Type: gent.LimitExactKey, Key: gent.SCIterations,
			MaxValue: scenario.MaxIterations}},
	}
	ConfigureCompaction(config, testCfg.Compaction, model, format.NewXML())
	if scenario.PolicySuggester != nil {
		hook := &PolicySuggestionHook{
			suggester: scenario.PolicySuggester, text: scenario.CustomerRequest,
		}
		config.Hooks = &gent.Hooks{BeforeModelCall: hook.BeforeModelCall}
	}
	runner := executor.NewRunner(react.NewAgent(model), config)
	execCtx, err := runner.NewExecutionContext(ctx, scenario.Name)
	if err != nil {
		return err
	}
	defer execCtx.Release()
	streamWait := StartTraceStreamOutput(seq, w)
	var logWait func()
	if testCfg.LogWriter != nil {
		logWait = StartTraceEventLogger(seq, testCfg.LogWriter)
	}
	task := &gent.Task{
		Instructions: []*gent.ModelMessage{gent.TextParts(gent.ChatMessageTypeSystem,
			scenario.SystemPrompt+"\n\n"+scenario.CriticalRules)},
		Messages: []*gent.ModelMessage{gent.TextParts(gent.ChatMessageTypeHuman,
			scenario.CustomerRequest)},
	}

	PrintHeader(w, scenario.HeaderTitle)
	fmt.Fprintln(w)

	if testCfg.Compaction.Type != CompactionNone &&
		testCfg.Compaction.Type != "" {
		PrintSection(w, "Compaction Config")
		fmt.Fprintf(w, "Strategy: %s\n", testCfg.Compaction.Type)
		fmt.Fprintf(w, "Trigger: every %d iterations\n", testCfg.Compaction.TriggerIterations)
		switch testCfg.Compaction.Type {
		case CompactionSlidingWindow:
			fmt.Fprintf(w, "Window size: %d\n", testCfg.Compaction.WindowSize)
		case CompactionSummarization:
			fmt.Fprintf(w, "Keep recent: %d\n", testCfg.Compaction.KeepRecent)
		}
		fmt.Fprintln(w)
	}

	PrintSection(w, "Customer Request")
	fmt.Fprintln(w, scenario.CustomerRequest)
	fmt.Fprintln(w)

	PrintSection(w, "Agent Execution")
	fmt.Fprintln(w)

	_, result, executionErr := runner.NewTurn(execCtx, task)
	seq.Close()
	streamWait()
	if logWait != nil {
		logWait()
		WriteTraceSnapshot(testCfg.LogWriter, seq.Snapshot())
	}
	if executionErr != nil {
		return executionErr
	}

	fmt.Fprintln(w)
	PrintHeader(w, "EXECUTION COMPLETE")
	fmt.Fprintln(w)

	if result.TerminationReason != gent.TerminationSuccess {
		fmt.Fprintf(w, "Execution: %s\n", result.TerminationReason)
	} else {
		PrintSection(w, "Final Response to Customer")
		for _, part := range result.Output {
			if tc, ok := part.(gent.TextContent); ok {
				fmt.Fprintln(w, tc.Text)
			}
		}
	}

	fmt.Fprintln(w)
	PrintSection(w, "Execution Stats")
	stats := execCtx.Stats()
	fmt.Fprintf(w, "Total iterations: %d\n", execCtx.Iteration())
	fmt.Fprintf(w, "Total input tokens: %d\n", stats.GetTotalInputTokens())
	fmt.Fprintf(w, "Total output tokens: %d\n", stats.GetTotalOutputTokens())
	fmt.Fprintf(w, "Total tool calls: %d\n", stats.GetToolCallCount())
	fmt.Fprintf(w, "Total compactions: %d\n", stats.GetCounter(gent.SCCompactions))
	fmt.Fprintf(w, "Duration: %v\n", execCtx.Duration())

	if testCfg.ShowIterationHistory {
		fmt.Fprintln(w)
		PrintHeader(w, "FULL ITERATION HISTORY")

		history := execCtx.History()
		for _, entry := range history.Entries {
			if entry.Kind == gent.HistoryCompaction {
				continue
			}
			fmt.Fprintf(w, "\n--- Iteration %d, record %s ---\n", entry.Iteration, entry.Id)
			for _, msg := range entry.Messages {
				fmt.Fprintf(w, "[%s]\n", msg.Role)
				for _, part := range msg.Parts {
					if tc, ok := part.(gent.TextContent); ok {
						text := tc.Text
						if len(text) > 3000 {
							text = text[:3000] +
								"\n... (truncated)"
						}
						fmt.Fprintln(w, text)
					}
				}
				fmt.Fprintln(w)
			}
		}
	}

	if testCfg.ShowEvents {
		fmt.Fprintln(w)
		PrintHeader(w, "TRACE EVENTS")
		printTraceEvents(w, seq.Snapshot().RecentEvents)
	}

	fmt.Fprintln(w)
	PrintHeader(w, "TEST COMPLETE")

	return nil
}

// -------------------------------------------------------------------------
// Shared chat configuration
// -------------------------------------------------------------------------

// ChatConfig defines the shared domain setup for the server chat and scenario tests.
type ChatConfig struct {
	Name          string
	SystemPrompt  string
	CriticalRules string
	MaxIterations float64
	RegisterTools func(tc gent.ToolChain)
	// PolicySuggester generates a policy suggestion section based
	// on conversation history. The server supplies recent saved messages as text.
	// Returns an empty string
	// if no policies are relevant.
	PolicySuggester func(ctx context.Context, text string) string
}

// serialWriter protects application writers that do not support concurrent calls.
type serialWriter struct {
	mu     *sync.Mutex
	target io.Writer
}

func (writer *serialWriter) Write(data []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.target.Write(data)
}
