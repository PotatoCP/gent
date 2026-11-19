package gent

import (
	"strings"
	"sync"
)

// StreamAccumulator joins deltas by logical item identity. It observes provider progress; only an
// accepted_answer RecordedEvent establishes acceptance. Response cannot replace native continuation
// from Stream.Response. A snapshot owns its mutable values so later deltas cannot change it.
type StreamAccumulator struct {
	mu    sync.Mutex
	parts []ContentPart
	items map[string]*assistantAccumulator
}

type assistantAccumulator struct {
	item *AssistantContent
	text strings.Builder
}

func NewStreamAccumulator() *StreamAccumulator { return &StreamAccumulator{} }

// Add records a progress delta. Equal item IDs refer to the same logical item, even when its
// classification becomes known late. The original frozen events remain unchanged.
func (a *StreamAccumulator) Add(chunk StreamChunk) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.items == nil {
		a.items = make(map[string]*assistantAccumulator)
	}
	for _, part := range chunk.Content {
		item, ok := part.(*AssistantContent)
		if !ok {
			a.parts = append(a.parts, cloneContentPart(part))
			continue
		}
		saved := a.items[item.MessageId]
		if saved == nil {
			saved = &assistantAccumulator{item: &AssistantContent{MessageId: item.MessageId}}
			a.items[item.MessageId] = saved
			a.parts = append(a.parts, saved.item)
		}
		saved.item.Type = item.Type
		for _, content := range item.Content {
			switch content := content.(type) {
			case TextContent:
				saved.text.WriteString(content.Text)
			case *TextContent:
				saved.text.WriteString(content.Text)
			default:
				saved.finishText()
				saved.item.Content = append(saved.item.Content, cloneContentPart(content))
			}
		}
	}
}

func (a *assistantAccumulator) finishText() {
	if a.text.Len() == 0 {
		return
	}
	a.item.Content = append(a.item.Content, TextContent{Text: a.text.String()})
	a.text.Reset()
}

// Content returns an independent snapshot in first-item order, retaining each item's content order.
func (a *StreamAccumulator) Content() []ContentPart {
	a.mu.Lock()
	defer a.mu.Unlock()
	result := CloneContentParts(a.parts)
	for _, part := range result {
		if item, ok := part.(*AssistantContent); ok {
			if saved := a.items[item.MessageId]; saved.text.Len() > 0 {
				item.Content = append(item.Content, TextContent{Text: saved.text.String()})
			}
		}
	}
	return result
}

// Response returns incomplete progress evidence. It does not infer an answer from intermediary
// text. The selected candidate, when present, is identified by its explicit classification and ID.
func (a *StreamAccumulator) Response() *ModelCallResponse {
	choice := &ContentChoice{Parts: a.Content(), Completion: ModelCompletion{
		Status: ModelStatusIncomplete}}
	for _, part := range choice.Parts {
		if item, ok := part.(*AssistantContent); ok &&
			item.Type == AssistantContentFinalAnswerCandidate {
			if choice.CandidateSource == nil {
				choice.CandidateSource = &ContentSource{}
			}
			choice.CandidateSource.MessageIds = append(choice.CandidateSource.MessageIds, item.MessageId)
		}
	}
	return &ModelCallResponse{Choices: []*ContentChoice{choice}}
}

// ResponseWithInfo retains final usage when a local size or repetition check stops a stream.
func (a *StreamAccumulator) ResponseWithInfo(response *ModelCallResponse) *ModelCallResponse {
	result := a.Response()
	if response != nil {
		result.Info = response.Info
	}
	return result
}

func (a *StreamAccumulator) Reset() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.parts, a.items = nil, nil
}
