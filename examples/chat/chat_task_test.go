package main

import (
	"context"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/integrationtest/testutil"
	"github.com/rickchristie/gent/toolchain"
	"github.com/stretchr/testify/assert"
)

type chatTaskModel struct{ messages [][]*gent.ModelMessage }

func (model *chatTaskModel) GenerateContentStream(
	_ context.Context, request *gent.ModelCallRequest,
) (gent.Stream, error) {
	model.messages = append(model.messages, request.Messages)
	return gent.NewCompletedStream(&gent.ModelCallResponse{Choices: []*gent.ContentChoice{{
		Completion:      gent.ModelCompletion{Status: gent.ModelStatusAnswer},
		CandidateSource: &gent.ContentSource{MessageIds: []string{"answer"}},
		Parts: []gent.ContentPart{&gent.AssistantContent{
			Type: gent.AssistantContentFinalAnswerCandidate, MessageId: "answer",
			Content: []gent.ContentPart{gent.TextPart("Done.")},
		}},
	}}, Info: &gent.GenerationInfo{}}, nil), nil
}

func (model *chatTaskModel) PrepareRequest(execCtx *gent.ExecutionContext,
	request *gent.ModelCallRequest, chain gent.ToolChain,
) (*gent.ModelCallRequest, error) {
	return gent.PrepareModelRequest(execCtx, request, chain)
}

type conversationMessage struct{ role, text string }

func TestChatHTTPKeepsMessageRoles(t *testing.T) {
	type input struct{ prompts []string }
	type expected struct{ history []conversationMessage }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"first turn", input{[]string{"Read."}}, expected{[]conversationMessage{
			{"user", "Read."}, {"agent", "Done."},
		}}},
		{"one conversation", input{[]string{"Earlier.", "Read.", "Check again."}},
			expected{[]conversationMessage{
				{"user", "Earlier."}, {"agent", "Done."}, {"user", "Read."}, {"agent", "Done."},
				{"user", "Check again."}, {"agent", "Done."},
			}}},
	}
	const system = `Help the customer.

Use supplied facts.`
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			model := &chatTaskModel{}
			app, _ := testApplication(t, 0)
			app.model = func() gent.Model { return model }
			app.newSession = func(id scenarioId) (*scenarioSession, error) {
				return &scenarioSession{id: id, tools: toolchain.New(), config: testutil.ChatConfig{
					Name: "roles", SystemPrompt: "Help the customer.", CriticalRules: "Use supplied facts.",
					MaxIterations:   3,
					PolicySuggester: func(context.Context, string) string { return "Check policy." },
				}}, nil
			}
			var history []conversationMessage
			var runId string
			conversation := []*gent.ModelMessage{
				gent.TextParts(gent.ChatMessageTypeSystem, system+"\n\n"+progressInstructions),
				gent.TextParts(gent.ChatMessageTypeSystem, "Check policy."),
			}
			var requests [][]*gent.ModelMessage
			for _, prompt := range test.input.prompts {
				conversation = append(conversation, gent.TextParts(gent.ChatMessageTypeHuman, prompt))
				requests = append(requests, append([]*gent.ModelMessage(nil), conversation...))
				next := submitHTTP(t, app, &actionRequest{Action: actionTurn,
					RunId: runId, Prompt: prompt})
				state := savedHTTP(t, app, next)
				if runId == "" {
					runId = next
				}
				assert.Equal(t, runId, next)
				history = append(history, conversationMessage{"user", prompt},
					conversationMessage{"agent", state.LastAnswer})
				data, _ := app.store.read(runId)
				runner := app.runner(nil, app.sessions[runId])
				execCtx, err := app.restore(app.ctx, runner, runId, data, replayCapture)
				if !assert.NoError(t, err) {
					return
				}
				defer execCtx.Release()
				// The saved native answer supplies the generated message identity. Verify all other
				// fields explicitly and require the next request to use this same logical item.
				entries, err := execCtx.Scratchpad()
				if !assert.NoError(t, err) {
					return
				}
				last := entries[len(entries)-1].Messages[0]
				item := last.Parts[0].(*gent.AssistantContent)
				answer := &gent.ModelMessage{Role: gent.ChatMessageTypeAI, Parts: []gent.ContentPart{
					&gent.AssistantContent{Type: gent.AssistantContentFinalAnswerCandidate,
						MessageId: item.MessageId, Content: []gent.ContentPart{gent.TextPart("Done.")}},
				}}
				assert.Equal(t, answer, &gent.ModelMessage{Role: last.Role, Parts: last.Parts,
					Continuation: last.Continuation})
				conversation = append(conversation, answer)
			}
			assert.Equal(t, requests, model.messages)
			assert.Equal(t, test.expected, expected{history})
		})
	}
}
