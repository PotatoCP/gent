package gent

import core "github.com/rickchristie/gent/internal/core"

// AssistantContentType identifies the purpose of one logical assistant item. Unknown provider
// labels remain readable; they never establish that Gent accepted an answer.
type AssistantContentType = core.AssistantContentType

const (
	// AssistantContentUnspecified means the item has no known classification.
	AssistantContentUnspecified AssistantContentType = core.AssistantContentUnspecified
	// AssistantContentCommentary identifies visible progress or commentary.
	AssistantContentCommentary AssistantContentType = core.AssistantContentCommentary
	// AssistantContentReasoning identifies exposed reasoning or its visible summary.
	AssistantContentReasoning AssistantContentType = core.AssistantContentReasoning
	// AssistantContentIntermediary identifies ordinary intermediate assistant content.
	AssistantContentIntermediary AssistantContentType = core.AssistantContentIntermediary
	// AssistantContentFinalAnswerCandidate identifies a proposed answer before acceptance.
	AssistantContentFinalAnswerCandidate AssistantContentType = AssistantContentType(
		core.AssistantContentFinalAnswerCandidate,
	)
	// AssistantContentFinalAnswer identifies complete output accepted and saved by Gent.
	AssistantContentFinalAnswer AssistantContentType = core.AssistantContentFinalAnswer
)

// AssistantContent is one logical assistant item with ordered text and media. The same MessageId
// identifies its deltas and complete response. The managed boundary scopes adapter-local IDs to a
// physical call; replay preserves them. Content cannot contain assistant groups or tool calls.
// Published values, including all reachable parts, are borrowed and read-only.
type AssistantContent = core.AssistantContent

// ContentSource links an accepted answer to its source previews. It is optional for a custom loop
// that produces output without a model. MessageIds must identify only the selected candidate.
// Stream labels group display output; the RecordedEvent envelope identifies the execution.
type ContentSource = core.ContentSource

// AssistantParts returns the inner parts of items with kind, in their original order. The result
// and its reachable values are borrowed and read-only. It can allocate a slice of references but
// does not copy text or media. Use CandidateContent to select an answer for output processing.
func AssistantParts(parts []ContentPart, kind AssistantContentType) []ContentPart {
	return core.AssistantParts(parts, kind)
}
