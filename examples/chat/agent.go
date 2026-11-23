package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/agents/react"
	"github.com/rickchristie/gent/output"
)

// Transformation changes only processed output. Gent retains the model's original candidate and
// links the accepted answer to it through ContentSource. The chat shows the accepted answer;
// the inspector retains the original candidate, including any removed whitespace.
type savedTextOutput struct{ *output.TextOutput }

func (out *savedTextOutput) Process(execCtx *gent.ExecutionContext, parts []gent.ContentPart) (
	*gent.OutputResult[string], error,
) {
	result, err := out.TextOutput.Process(execCtx, parts)
	if err != nil || !result.Accepted {
		return result, err
	}
	text := strings.TrimSpace(result.Value)
	return &gent.OutputResult[string]{Accepted: true, Value: text,
		Content: []gent.ContentPart{gent.TextPart(text)}}, nil
}

func newAgent(model gent.Model, provider string) *react.Agent[string] {
	return react.NewWithOutput(model, &savedTextOutput{output.NewTextOutput()}).
		WithCallOptions(chatCallOptions(provider, model))
}

// The deterministic provider exercises the same managed stream, native tool, and output paths.
// It is labelled as a demo in the UI. Its reasoning text is fixture data, not a remote model trace.
type demoModel struct{ delay time.Duration }

func (model *demoModel) PrepareRequest(execCtx *gent.ExecutionContext,
	request *gent.ModelCallRequest, tools gent.ToolChain,
) (*gent.ModelCallRequest, error) {
	return gent.PrepareModelRequest(execCtx, request, tools)
}

func (model *demoModel) GenerateContentStream(ctx context.Context, request *gent.ModelCallRequest) (
	gent.Stream, error,
) {
	var first, latest, result string
	email := "john.smith@email.com"
	for _, message := range request.Messages {
		switch message.Role {
		case gent.ChatMessageTypeSystem:
			if strings.Contains(gent.ContentText(message.Parts), "TechGear") {
				email = "alex.rivera@email.com"
			}
		case gent.ChatMessageTypeHuman:
			latest = gent.ContentText(message.Parts)
			if first == "" {
				first = latest
			}
		case gent.ChatMessageTypeTool:
			for _, part := range message.Parts {
				if reply, ok := part.(*gent.ModelToolCallResponse); ok {
					for _, part := range reply.Parts() {
						if data, ok := part.(gent.DataContent); ok {
							customer := data.Value().(map[string]any)
							result = fmt.Sprintf("I found %s (%s), using %s.",
								customer["name"], customer["id"], customer["email"])
						}
					}
				}
			}
		}
	}
	if result == "" {
		return gent.NewCompletedStream(&gent.ModelCallResponse{Choices: []*gent.ContentChoice{{
			Completion: gent.ModelCompletion{Status: gent.ModelStatusToolCalls},
			Parts: []gent.ContentPart{&gent.AssistantContent{
				Type: gent.AssistantContentCommentary, MessageId: "lookup-progress",
				Content: []gent.ContentPart{gent.TextPart("I'll check the customer record first.")},
			}, &gent.ModelToolCallRequest{ID: "customer-read", Type: "function",
				FunctionCall: &gent.FunctionCall{Name: "get_customer_info",
					Arguments: fmt.Sprintf(`{"email":%q}`, email)},
			}},
		}}}, nil), nil
	}
	answer := result
	if latest != first {
		answer = "Your first message was: " + first + " Earlier lookup: " + result
	}
	return model.streamAnswer(ctx, "\n"+answer+"\n")
}

func (model *demoModel) streamAnswer(parent context.Context, answer string) (gent.Stream, error) {
	ctx, cancel := context.WithCancel(parent)
	stream, err := gent.NewBoundedStreamWithDuration(ctx, 16, cancel)
	if err != nil {
		cancel()
		return nil, err
	}
	go func() {
		defer cancel()
		reasoning := "The saved lookup supplies the customer facts. Earlier user input is still present."
		groups := []*gent.AssistantContent{
			{Type: gent.AssistantContentReasoning, MessageId: "reasoning",
				Content: []gent.ContentPart{gent.TextPart(reasoning)}},
			{Type: gent.AssistantContentFinalAnswerCandidate, MessageId: "answer",
				Content: []gent.ContentPart{gent.TextPart(answer)}},
		}
		for _, group := range groups {
			words := strings.Split(gent.ContentText(group.Content), " ")
			for index, word := range words {
				timer := time.NewTimer(model.delay)
				select {
				case <-ctx.Done():
					timer.Stop()
					stream.Complete(nil, ctx.Err())
					return
				case <-timer.C:
				}
				if index < len(words)-1 {
					word += " "
				}
				if !stream.Send(gent.StreamChunk{Content: []gent.ContentPart{&gent.AssistantContent{
					Type: group.Type, MessageId: group.MessageId,
					Content: []gent.ContentPart{gent.TextPart(word)},
				}}}) {
					stream.Complete(nil, context.Canceled)
					return
				}
			}
		}
		stream.Complete(&gent.ModelCallResponse{Choices: []*gent.ContentChoice{{
			Completion:      gent.ModelCompletion{Status: gent.ModelStatusAnswer},
			Parts:           []gent.ContentPart{groups[0], groups[1]},
			CandidateSource: &gent.ContentSource{MessageIds: []string{"answer"}},
		}}}, nil)
	}()
	return stream, nil
}
