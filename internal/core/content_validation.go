package gent

import "fmt"

// Keep invalid adapter output out of dependent tools and output processing. Native final phases
// map to candidates in adapters; they cannot grant Gent acceptance at this boundary.
func validateAssistantItem(item *AssistantContent, provider bool) error {
	if item.MessageId == "" {
		return fmt.Errorf("gent: assistant item has no MessageId")
	}
	if provider && item.Type == AssistantContentFinalAnswer {
		return fmt.Errorf("gent: provider item %q cannot establish an accepted answer", item.MessageId)
	}
	for _, part := range item.Content {
		if err := validateAssistantInner(part); err != nil {
			return fmt.Errorf("gent: assistant item %q: %w", item.MessageId, err)
		}
	}
	return nil
}

func validateAssistantInner(part ContentPart) error {
	switch part := part.(type) {
	case TextContent, *TextContent, BinaryContent, *BinaryContent,
		ImageURLContent, *ImageURLContent, DataContent, *DataContent:
		return nil
	case CachedContent:
		return validateAssistantInner(part.ContentPart)
	case *CachedContent:
		return validateAssistantInner(part.ContentPart)
	default:
		return fmt.Errorf("nested assistant content or embedded tool part %T is not allowed", part)
	}
}

func validateAssistantParts(parts []ContentPart, provider bool) error {
	for _, part := range parts {
		switch part := part.(type) {
		case *AssistantContent:
			if err := validateAssistantItem(part, provider); err != nil {
				return err
			}
		case *ModelToolCallRequest, ModelToolCallRequest:
		default:
			return fmt.Errorf("gent: assistant response requires a group or tool call, got %T", part)
		}
	}
	return nil
}

func (choice *ContentChoice) validateParts() error {
	if err := validateAssistantParts(choice.Parts, true); err != nil {
		return err
	}
	items := make(map[string]*AssistantContent)
	for _, part := range choice.Parts {
		if item, ok := part.(*AssistantContent); ok {
			if _, exists := items[item.MessageId]; exists {
				return fmt.Errorf("gent: duplicate assistant item %q", item.MessageId)
			}
			items[item.MessageId] = item
		}
	}
	if choice.CandidateSource == nil {
		return nil
	}
	selected := map[string]bool{}
	for _, id := range choice.CandidateSource.MessageIds {
		item := items[id]
		if item == nil || selected[id] || item.Type != AssistantContentFinalAnswerCandidate {
			return fmt.Errorf("gent: invalid candidate source %q: missing, repeated, or not a candidate", id)
		}
		selected[id] = true
	}
	return nil
}

// Plain application messages remain supported. Assistant groups in requests retain classification
// and identity; reject nested groups before an adapter can flatten invalid state into a request.
func validateRequestGroups(request *ModelCallRequest) error {
	for i, message := range request.Messages {
		for _, part := range message.Parts {
			if item, ok := part.(*AssistantContent); ok {
				if err := validateAssistantItem(item, false); err != nil {
					return fmt.Errorf("gent: model request message %d: %w", i, err)
				}
			}
		}
	}
	return nil
}
