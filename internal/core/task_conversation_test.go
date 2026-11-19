package gent

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewTurnAcceptsCompleteConversationContext(t *testing.T) {
	type input struct{ messages []*ModelMessage }
	type expected struct {
		err         string
		recordSizes []int
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	user := TextParts(ChatMessageTypeHuman, "Continue this work.")
	item := &AssistantContent{MessageId: "answer-1", Type: AssistantContentFinalAnswer,
		Content: []ContentPart{TextPart("Earlier answer"), PDFPart([]byte("report"))}}
	assistant := &ModelMessage{Role: ChatMessageTypeAI, Parts: []ContentPart{item},
		Continuation: &ProviderContinuation{Provider: "test", Items: []json.RawMessage{
			json.RawMessage(`{"native":"preserve"}`)}}}
	call := ModelToolCallRequest{ID: "call-1", Type: "function",
		FunctionCall: &FunctionCall{Name: "lookup", Arguments: `{"id":"O1"}`}}
	record, err := NewToolCallRecordBuilder("operation-1", "old-attempt", "lookup").
		Execution(&ToolExecutionResponse{Status: ToolExecuted,
			Parts: []ContentPart{TextPart("Found O1")}}).
		ModelReply("call-1", "lookup", TextPart("Found O1")).Build()
	if !assert.NoError(t, err) {
		return
	}
	reply := &ModelMessage{Role: ChatMessageTypeTool, Parts: []ContentPart{record.Response()}}
	request := &ModelMessage{Role: ChatMessageTypeAI, Parts: []ContentPart{item, &call}}
	cases := []testCase{
		{"plain assistant", input{[]*ModelMessage{user,
			TextParts(ChatMessageTypeAI, "Earlier answer")}}, expected{recordSizes: []int{1, 1}}},
		{"assistant group and native context", input{[]*ModelMessage{assistant, user}},
			expected{recordSizes: []int{1, 1}}},
		{"pointer tool request", input{[]*ModelMessage{user, request, reply}},
			expected{recordSizes: []int{1, 2}}},
		{"value tool request", input{[]*ModelMessage{user,
			{Role: ChatMessageTypeAI, Parts: []ContentPart{call}}, reply}},
			expected{recordSizes: []int{1, 2}}},
		{"assistant group in user message", input{[]*ModelMessage{
			{Role: ChatMessageTypeHuman, Parts: []ContentPart{item}}}},
			expected{err: "Messages[0]: assistant content requires an assistant message"}},
		{"request in user message", input{[]*ModelMessage{
			{Role: ChatMessageTypeHuman, Parts: []ContentPart{&call}}}},
			expected{err: "Messages[0]: tool request requires an assistant message"}},
		{"unanswered request", input{[]*ModelMessage{user, request}},
			expected{err: "Task.Messages has unanswered tool calls: call-1"}},
		{"orphan response", input{[]*ModelMessage{user, reply}},
			expected{err: `Messages[1]: tool response "call-1" has no preceding request`}},
		{"plain tool text", input{[]*ModelMessage{user,
			TextParts(ChatMessageTypeTool, "Earlier result")}},
			expected{err: "Messages[1]: tool message requires a correlated tool response"}},
		{"empty tool message", input{[]*ModelMessage{user, {Role: ChatMessageTypeTool}}},
			expected{err: "Messages[1]: tool message requires a correlated tool response"}},
		{"text before a correlated reply", input{[]*ModelMessage{user, request,
			{Role: ChatMessageTypeTool, Parts: []ContentPart{TextPart("Earlier result"),
				record.Response()}}}},
			expected{err: "Messages[2]: tool message requires a correlated tool response"}},
		{"duplicate response", input{[]*ModelMessage{user, request, reply, reply}},
			expected{err: `Messages[3]: tool response "call-1" has no preceding request`}},
		{"response in assistant message", input{[]*ModelMessage{user, request,
			{Role: ChatMessageTypeAI, Parts: reply.Parts}}},
			expected{err: "Messages[2]: tool response requires a tool message"}},
		{"missing assistant identity", input{[]*ModelMessage{user,
			{Role: ChatMessageTypeAI, Parts: []ContentPart{&AssistantContent{}}}}},
			expected{err: "Messages[1]: gent: assistant item has no MessageId"}},
		{"nested assistant content", input{[]*ModelMessage{user,
			{Role: ChatMessageTypeAI, Parts: []ContentPart{&AssistantContent{
				MessageId: "outer", Content: []ContentPart{item}}}}}},
			expected{err: `Messages[1]: gent: assistant item "outer": ` +
				"nested assistant content or embedded tool part *gent.AssistantContent is not allowed"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			codecs := NewCodecRegistry()
			store := &runStateRecorder{codecs: codecs, capture: true}
			model := &requestOwnershipModel{}
			task := &Task{Messages: test.input.messages}
			loop := &turnTestLoop{next: func(execCtx *ExecutionContext) (*AgentLoopResult, error) {
				entries, err := execCtx.Scratchpad()
				if err != nil {
					return nil, err
				}
				var sizes []int
				for _, entry := range entries {
					sizes = append(sizes, len(entry.Messages))
				}
				assert.Equal(t, test.expected.recordSizes, sizes)
				owned, err := execCtx.Task()
				if err != nil {
					return nil, err
				}
				stream, err := execCtx.GenerateContentStream(model, ModelCall{},
					NewModelCallRequest(owned.Messages))
				if err != nil {
					return nil, err
				}
				defer stream.Close()
				if _, err := stream.Response(); err != nil {
					return nil, err
				}
				return acceptTurnText(execCtx, "Done")
			}}
			runner := NewRunner[string](loop, &Config{Codecs: codecs, Recorder: store})
			execCtx, err := runner.NewExecutionContext(context.Background(), test.name)
			if !assert.NoError(t, err) {
				return
			}
			defer execCtx.Release()
			value, result, err := runner.NewTurn(execCtx, task)
			if test.expected.err != "" {
				assert.EqualError(t, err, fmt.Sprintf("gent: validate NewTurn input for run %q: %s",
					execCtx.RunId(), test.expected.err))
				assert.Equal(t, "", value)
				assert.Nil(t, result)
				assert.Empty(t, store.batches)
				assert.Nil(t, model.request)
				return
			}
			if !assert.NoError(t, err) {
				return
			}
			assert.Equal(t, "Done", value)
			assert.Equal(t, test.input.messages, model.request.Messages)
			assert.Equal(t, &ExecutionResult{Status: ExecutionFinished, TurnId: execCtx.TurnId(),
				TerminationReason: TerminationSuccess, Output: []ContentPart{TextPart("Done")}}, result)
			state := NewRunState(execCtx.RunId())
			for _, data := range store.batches {
				applyRecordedState(t, state, codecs, data)
			}
			assert.Equal(t, encodeRunState(t, codecs, state),
				encodeRunState(t, codecs, store.states[len(store.states)-1]))
			restored, err := runner.Restore(context.Background(), state)
			if !assert.NoError(t, err) || !assert.Equal(t, RestoreReady, restored.Status) {
				return
			}
			defer restored.Context.Release()
			loaded, err := restored.Context.Task()
			assert.NoError(t, err)
			assert.Equal(t, task, loaded)
		})
	}
}
