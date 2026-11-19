package gent

// AssistantContentType identifies the purpose of one logical assistant item. Unknown provider
// labels remain readable; they never establish that Gent accepted an answer.
type AssistantContentType string

const (
	// AssistantContentUnspecified means the item has no known classification.
	AssistantContentUnspecified AssistantContentType = ""
	// AssistantContentCommentary identifies visible progress or commentary.
	AssistantContentCommentary AssistantContentType = "commentary"
	// AssistantContentReasoning identifies exposed reasoning or its visible summary.
	AssistantContentReasoning AssistantContentType = "reasoning"
	// AssistantContentIntermediary identifies ordinary intermediate assistant content.
	AssistantContentIntermediary AssistantContentType = "intermediary"
	// AssistantContentFinalAnswerCandidate identifies a proposed answer before acceptance.
	AssistantContentFinalAnswerCandidate AssistantContentType = "final_answer_candidate"
	// AssistantContentFinalAnswer identifies complete output accepted and saved by Gent.
	AssistantContentFinalAnswer AssistantContentType = "final_answer"
)

// AssistantContent is one logical assistant item with ordered text and media. The same MessageId
// identifies its deltas and complete response. The managed boundary scopes adapter-local IDs to a
// physical call; replay preserves them. Content cannot contain assistant groups or tool calls.
// Published values, including all reachable parts, are borrowed and read-only.
type AssistantContent struct {
	Type      AssistantContentType
	MessageId string
	Content   []ContentPart
}

func (*AssistantContent) isContentPart() {}

// AssistantParts returns the inner parts of items with kind, in their original order. The result
// and its reachable values are borrowed and read-only. It can allocate a slice of references but
// does not copy text or media. Use CandidateContent to select an answer for output processing.
func AssistantParts(parts []ContentPart, kind AssistantContentType) []ContentPart {
	var result []ContentPart
	for _, part := range parts {
		if item, ok := part.(*AssistantContent); ok && item.Type == kind {
			result = append(result, item.Content...)
		}
	}
	return result
}

// ContentSource links an accepted answer to its source previews. It is optional for a custom loop
// that produces output without a model. MessageIds must identify only the selected candidate.
// Stream labels group display output; the RecordedEvent envelope identifies the execution.
type ContentSource struct {
	ModelCallId   string
	StreamId      string
	StreamTopicId string
	MessageIds    []string
}

func cloneContentSource(source *ContentSource) *ContentSource {
	if source == nil {
		return nil
	}
	result := *source
	result.MessageIds = append([]string(nil), source.MessageIds...)
	return &result
}

func cloneStreamChunk(chunk StreamChunk) StreamChunk {
	chunk.Content = CloneContentParts(chunk.Content)
	chunk.SourceMessageIds = append([]string(nil), chunk.SourceMessageIds...)
	return chunk
}
