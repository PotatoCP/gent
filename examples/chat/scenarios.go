package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/integrationtest/airline"
	"github.com/rickchristie/gent/integrationtest/ecommerce"
	"github.com/rickchristie/gent/integrationtest/testutil"
	"github.com/rickchristie/gent/search"
	"github.com/rickchristie/gent/toolchain"
)

type scenarioId string

const (
	// scenarioAirline uses the SkyWings booking and policy fixture from the integration tests.
	scenarioAirline scenarioId = "airline"
	// scenarioEcommerce uses the TechGear billing and policy fixture from the integration tests.
	scenarioEcommerce scenarioId = "ecommerce"
)

type scenarioInfo struct {
	Id          scenarioId `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Prompt      string     `json:"prompt"`
}

var scenarios = []scenarioInfo{
	{scenarioAirline, "SkyWings Airlines", "Change a flight booking", airline.CustomerRequest},
	{scenarioEcommerce, "TechGear Store", "Investigate a duplicate charge", ecommerce.CustomerRequest},
}

// This fixture represents the external service, not agent state. Keep it across worker restores.
// Only one worker can use a conversation at a time. Other conversations have separate fixtures.
// A server restart clears these simulated services together with the byte store.
type scenarioSession struct {
	id     scenarioId
	config testutil.ChatConfig
	tools  gent.ToolChain
}

func newScenario(id scenarioId, embedder search.Embedder) (*scenarioSession, error) {
	config := testutil.TestConfig{Embedder: embedder, ToolChain: testutil.ToolChainRegistry}
	var chat testutil.ChatConfig
	switch id {
	case scenarioAirline:
		chat = airline.NewChatConfig(config)
	case scenarioEcommerce:
		chat = ecommerce.NewChatConfig(config)
	default:
		return nil, fmt.Errorf("unknown scenario %q", id)
	}
	chain := toolchain.New()
	chat.RegisterTools(chain)
	return &scenarioSession{id: id, config: chat, tools: chain}, nil
}

func (session *scenarioSession) instructions() string {
	return session.config.SystemPrompt + "\n\n" + session.config.CriticalRules
}

// The same policy suggester supplies guidance in the CLI. It changes the effective request only.
// Derive its input from restored conversation messages so Continue needs no process-local history.
func (session *scenarioSession) hooks() *gent.Hooks {
	calls, suggestion := 0, ""
	return &gent.Hooks{BeforeModelCall: func(
		ctx context.Context, event *gent.BeforeModelCallEvent,
	) error {
		event.Request.Options.MaxTokens = 4096
		if event.StreamTopicId != "llm-response" || calls >= 3 {
			return nil
		}
		if calls == 0 {
			var messages []string
			for _, message := range event.Request.Messages {
				if message.Role == gent.ChatMessageTypeHuman || message.Role == gent.ChatMessageTypeAI {
					messages = append(messages, gent.ContentText(message.Parts))
				}
			}
			if len(messages) > 10 {
				messages = messages[len(messages)-10:]
			}
			suggestion = session.config.PolicySuggester(ctx, strings.Join(messages, "\n"))
		}
		calls++
		if suggestion != "" {
			event.Request.Messages = append(event.Request.Messages,
				gent.TextParts(gent.ChatMessageTypeSystem, suggestion))
		}
		return nil
	}}
}
