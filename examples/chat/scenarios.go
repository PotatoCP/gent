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
	Id            scenarioId `json:"id"`
	Name          string     `json:"name"`
	Description   string     `json:"description"`
	Prompt        string     `json:"prompt"`
	Investigation string     `json:"investigation"`
}

var scenarios = []scenarioInfo{
	{scenarioAirline, "SkyWings Airlines", "Change a flight booking", airline.CustomerRequest,
		airline.InvestigationRequest},
	{scenarioEcommerce, "TechGear Store", "Investigate a duplicate charge", ecommerce.CustomerRequest,
		ecommerce.InvestigationRequest},
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

const progressInstructions = `Give brief updates before tool work and when a finding changes
the next action. State what you will check or what you found. Continue with the tools in the same
response; a progress update alone does not complete the task. Keep the final customer answer
separate. Do not disclose private reasoning or invent facts to fill a progress update.`

func (session *scenarioSession) instructions() string {
	return session.config.SystemPrompt + "\n\n" + session.config.CriticalRules +
		"\n\n" + progressInstructions
}

// The shared scenario policy suggester changes the effective request only.
// Derive its input from restored conversation messages so Continue needs no process-local history.
func (session *scenarioSession) hooks() *gent.Hooks {
	calls, suggestion := 0, ""
	return &gent.Hooks{BeforeModelCall: func(
		ctx context.Context, event *gent.BeforeModelCallEvent,
	) error {
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
			// Native Anthropic and Gemini requests require system messages in the prefix. Keep
			// policy guidance after the original instructions, without changing saved user history.
			prefix := 0
			for prefix < len(event.Request.Messages) &&
				event.Request.Messages[prefix].Role == gent.ChatMessageTypeSystem {
				prefix++
			}
			messages := make([]*gent.ModelMessage, 0, len(event.Request.Messages)+1)
			messages = append(messages, event.Request.Messages[:prefix]...)
			messages = append(messages, gent.TextParts(gent.ChatMessageTypeSystem, suggestion))
			event.Request.Messages = append(messages, event.Request.Messages[prefix:]...)
		}
		return nil
	}}
}
