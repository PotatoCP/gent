package testutil

import (
	"bytes"
	"context"
	"testing"

	"github.com/rickchristie/gent"
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

func TestInteractiveChatKeepsMessageRoles(t *testing.T) {
	type input struct{ prompts []string }
	type expected struct{ history []ConversationMessage }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"first turn", input{[]string{"Read."}}, expected{[]ConversationMessage{
			{"user", "Read."}, {"agent", "Done."},
		}}},
		{"one conversation", input{[]string{"Earlier.", "Read.", "Check again."}},
			expected{[]ConversationMessage{
				{"user", "Earlier."}, {"agent", "Done."}, {"user", "Read."}, {"agent", "Done."},
				{"user", "Check again."}, {"agent", "Done."},
			}}},
	}
	const system = `Help the customer.

Use supplied facts.`
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			model := &chatTaskModel{}
			var rendered bytes.Buffer
			chat := &InteractiveChat{Model: model, Writer: &rendered,
				Config: TestConfig{ToolChain: ToolChainRegistry}, ChatCfg: ChatConfig{
					Name: "roles", SystemPrompt: "Help the customer.", CriticalRules: "Use supplied facts.",
					MaxIterations: 3, RegisterTools: func(gent.ToolChain) {},
				}}
			t.Cleanup(chat.Close)
			var runId string
			conversation := []*gent.ModelMessage{gent.TextParts(gent.ChatMessageTypeSystem, system)}
			var requests [][]*gent.ModelMessage
			for _, prompt := range test.input.prompts {
				conversation = append(conversation, gent.TextParts(gent.ChatMessageTypeHuman, prompt))
				requests = append(requests, append([]*gent.ModelMessage(nil), conversation...))
				if !assert.NoError(t, chat.SendMessage(context.Background(), prompt)) {
					return
				}
				if runId == "" {
					runId = chat.execCtx.RunId()
				}
				assert.Equal(t, runId, chat.execCtx.RunId())
				// The saved native answer supplies the generated message identity. Verify all other
				// fields explicitly and require the next request to use this same logical item.
				entries, err := chat.execCtx.Scratchpad()
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
			chat.Close()
			assert.Equal(t, requests, model.messages)
			assert.Equal(t, test.expected, expected{chat.History})
		})
	}
}
