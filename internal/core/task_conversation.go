package gent

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// A task can contain prior conversation, including complete native call/reply groups. Its
// messages are not tool-return content. Validate their roles and correlations without changing
// assistant classification or opaque provider evidence; adapters validate native details later.
func validateTaskConversation(messages []*ModelMessage) error {
	pending := map[string]string{}
	seen := map[string]bool{}
	for i, message := range messages {
		if message.Role == ChatMessageTypeTool && len(message.Parts) == 0 {
			return fmt.Errorf("Messages[%d]: tool message requires a correlated tool response", i)
		}
		if message.Role == ChatMessageTypeHuman && len(pending) != 0 {
			return fmt.Errorf("Messages[%d]: user input interrupts unanswered tool calls: %s",
				i, taskCallIds(pending))
		}
		for _, part := range message.Parts {
			if err := validateTaskPart(message.Role, part, pending, seen); err != nil {
				return fmt.Errorf("Messages[%d]: %w", i, err)
			}
		}
	}
	if len(pending) != 0 {
		return fmt.Errorf("Task.Messages has unanswered tool calls: %s", taskCallIds(pending))
	}
	return nil
}

func validateTaskPart(role ChatMessageType, part ContentPart, pending map[string]string,
	seen map[string]bool,
) error {
	if part == nil || (reflect.ValueOf(part).Kind() == reflect.Pointer &&
		reflect.ValueOf(part).IsNil()) {
		return fmt.Errorf("content part is nil")
	}
	switch part := part.(type) {
	case *AssistantContent:
		if role != ChatMessageTypeAI {
			return fmt.Errorf("assistant content requires an assistant message")
		}
		return validateAssistantItem(part, false)
	case ModelToolCallRequest:
		return validateTaskCall(role, &part, pending, seen)
	case *ModelToolCallRequest:
		return validateTaskCall(role, part, pending, seen)
	case *ModelToolCallResponse:
		if role != ChatMessageTypeTool {
			return fmt.Errorf("tool response requires a tool message")
		}
		name, exists := pending[part.ToolCallID()]
		if !exists {
			return fmt.Errorf("tool response %q has no preceding request", part.ToolCallID())
		}
		if name != part.Name() {
			return fmt.Errorf("tool response %q names %q, expected %q",
				part.ToolCallID(), part.Name(), name)
		}
		if err := ValidateContentParts(part.Parts()); err != nil {
			return err
		}
		delete(pending, part.ToolCallID())
		return nil
	default:
		if role == ChatMessageTypeTool {
			return fmt.Errorf("tool message requires a correlated tool response")
		}
		return ValidateContentParts([]ContentPart{part})
	}
}

func validateTaskCall(role ChatMessageType, call *ModelToolCallRequest, pending map[string]string,
	seen map[string]bool,
) error {
	if role != ChatMessageTypeAI {
		return fmt.Errorf("tool request requires an assistant message")
	}
	if call.ID == "" || call.FunctionCall == nil || call.FunctionCall.Name == "" {
		return fmt.Errorf("tool request requires a call ID and function name")
	}
	if seen[call.ID] {
		return fmt.Errorf("duplicate tool request %q", call.ID)
	}
	seen[call.ID] = true
	pending[call.ID] = call.FunctionCall.Name
	return nil
}

func taskCallIds(pending map[string]string) string {
	ids := make([]string, 0, len(pending))
	for id := range pending {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return strings.Join(ids, ", ")
}

// Validation has proved that calls have complete replies. Keep each group in one record so
// record-based compaction cannot retain a reply after removing its request. User input keeps its
// own saved origin and does not become eligible for retention merely because another role has text.
func (execCtx *ExecutionContext) appendTaskRecords(messages []*ModelMessage) error {
	var entry *HistoryEntry
	pending := 0
	for _, message := range messages {
		if entry == nil {
			entry = &HistoryEntry{Origin: RecordOriginal}
			if message.Role == ChatMessageTypeHuman {
				entry.Origin = RecordUserInput
			}
		}
		entry.Messages = append(entry.Messages, &MessageContent{Role: message.Role,
			Parts: message.Parts, Continuation: message.Continuation})
		for _, part := range message.Parts {
			switch part.(type) {
			case ModelToolCallRequest, *ModelToolCallRequest:
				pending++
			case *ModelToolCallResponse:
				pending--
			}
		}
		if pending == 0 {
			if err := execCtx.AppendRecord(entry, true); err != nil {
				return err
			}
			entry = nil
		}
	}
	return nil
}
