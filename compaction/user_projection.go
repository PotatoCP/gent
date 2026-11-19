package compaction

import (
	"fmt"
	"reflect"
	"unicode/utf8"

	"github.com/rickchristie/gent"
)

const retainedUserKey gent.RecordMetadataKey = "gent:retained_user"
const retentionNoticeKey gent.RecordMetadataKey = "gent:user_retention_notice"
const retentionDecisionKey gent.RecordMetadataKey = "gent:user_retention_decision"

// WithUserMessageRetention selects the standard summarizer's one retention policy. Configuration
// is read-only during use. Invalid allowances are rejected before a summary model call. Retention
// runs only at compaction boundaries, preserving append-only request growth between compactions.
func (s *SummarizationStrategy) WithUserMessageRetention(
	config *gent.UserMessageRetentionConfig,
) *SummarizationStrategy {
	s.userRetention = config
	return s
}

func (s *SummarizationStrategy) retentionInput(input *gent.CompactionInput) (
	*gent.UserMessageRetentionInput, gent.UserMessageRetentionStrategy, map[gent.EntryId]bool, error,
) {
	config := gent.UserMessageRetentionConfig{}
	if s.userRetention != nil {
		config = *s.userRetention
	}
	if config.MaxBytes < 0 || s.keepRecent < 0 {
		return nil, nil, nil, fmt.Errorf("gent: retention allowance and recent window cannot be negative")
	}
	if config.MaxBytes == 0 {
		config.MaxBytes = defaultUserRetentionBytes
	}
	if config.TruncationMarker == "" {
		config.TruncationMarker = defaultUserRetentionMarker
	}
	if !utf8.ValidString(config.TruncationMarker) {
		return nil, nil, nil, fmt.Errorf("gent: user retention notice must be valid UTF-8")
	}
	if config.MaxBytes < len(config.TruncationMarker) {
		return nil, nil, nil, fmt.Errorf("gent: user retention allowance %d must fit the %d-byte notice",
			config.MaxBytes, len(config.TruncationMarker))
	}
	if config.Strategy == nil {
		config.Strategy = boundaryUserRetention{}
	}
	retention := &gent.UserMessageRetentionInput{
		MaxBytes: config.MaxBytes,
		Marker:   config.TruncationMarker,
	}
	users := map[gent.EntryId]bool{}
	for _, entry := range input.Entries {
		if _, exists := entry.GetMetadata(retentionNoticeKey); exists {
			retention.EarlierContentOmitted = true
		}
		if value, found := entry.GetMetadata(retainedUserKey); found {
			projection, ok := value.(*gent.UserMessageRetentionEntry)
			if !ok {
				return nil, nil, nil, fmt.Errorf("gent: invalid retained user source in %q", entry.Id)
			}
			retention.Messages = append(retention.Messages, projection)
			users[entry.Id] = true
			continue
		}
		if entry.Origin != gent.RecordUserInput {
			continue
		}
		for i, message := range entry.Messages {
			if message.Role != gent.ChatMessageTypeHuman {
				continue
			}
			retention.Messages = append(retention.Messages, &gent.UserMessageRetentionEntry{
				EntryId: entry.Id, MessageIndex: i, Message: &gent.ModelMessage{
					Role: message.Role, Parts: message.Parts, Continuation: message.Continuation}})
			users[entry.Id] = true
		}
	}
	// Check every input part before spending model work. The custom strategy must observe the same
	// framework byte accounting, even if it chooses whole messages or a different selection.
	for _, entry := range retention.Messages {
		for _, part := range entry.Message.Parts {
			if _, err := retentionPartBytes(part); err != nil {
				return nil, nil, nil, err
			}
		}
	}
	return retention, config.Strategy, users, nil
}

func validateUserRetention(
	input *gent.UserMessageRetentionInput,
	result *gent.UserMessageRetentionResult,
) error {
	if result == nil {
		return fmt.Errorf("gent: user retention returned no result")
	}
	type source struct {
		id    gent.EntryId
		index int
	}
	positions := map[source]int{}
	for i, entry := range input.Messages {
		positions[source{entry.EntryId, entry.MessageIndex}] = i
	}
	previous, size := -1, 0
	omitted := input.EarlierContentOmitted || len(result.Messages) != len(input.Messages)
	if result.EarlierContentOmitted {
		size = len(input.Marker)
	}
	for _, entry := range result.Messages {
		if entry == nil || entry.Message == nil {
			return fmt.Errorf("gent: nil retained user projection")
		}
		position, found := positions[source{entry.EntryId, entry.MessageIndex}]
		if !found || position <= previous {
			return fmt.Errorf("gent: invalid retained user source order")
		}
		previous = position
		original := input.Messages[position]
		changed := !reflect.DeepEqual(original.Message, entry.Message)
		if entry.Message.Role != gent.ChatMessageTypeHuman ||
			(original.Truncated || changed) && !entry.Truncated {
			return fmt.Errorf("gent: retained user projection lost its role or truncation state")
		}
		omitted = omitted || changed || entry.Truncated
		for _, part := range entry.Message.Parts {
			bytes, err := retentionPartBytes(part)
			if err != nil {
				return err
			}
			if bytes > input.MaxBytes-size {
				return fmt.Errorf("gent: user retention exceeded its allowance")
			}
			size += bytes
		}
	}
	if omitted != result.EarlierContentOmitted || size != result.RetainedBytes ||
		size > input.MaxBytes {
		return fmt.Errorf("gent: user retention returned inconsistent omission or byte accounting")
	}
	return nil
}

func retainedUserEntries(execCtx *gent.ExecutionContext, input *gent.UserMessageRetentionInput,
	result *gent.UserMessageRetentionResult,
) ([]*gent.HistoryEntry, error) {
	var entries []*gent.HistoryEntry
	for i, projection := range result.Messages {
		message := projection.Message
		parts := message.Parts
		if i == 0 && result.EarlierContentOmitted {
			parts = append([]gent.ContentPart{gent.TextPart(input.Marker)}, parts...)
		}
		entry := &gent.HistoryEntry{Origin: gent.RecordCompactedModified,
			Replaces: []gent.EntryId{projection.EntryId},
			Messages: []*gent.MessageContent{{Role: message.Role, Parts: parts,
				Continuation: message.Continuation}}}
		entry.SetMetadata(retainedUserKey, projection)
		if i == 0 && result.EarlierContentOmitted {
			entry.SetMetadata(retentionNoticeKey, input.Marker)
		}
		owned, err := execCtx.NewHistoryEntry(entry)
		if err != nil {
			return nil, err
		}
		entries = append(entries, owned)
	}
	if len(entries) == 0 && result.EarlierContentOmitted {
		entry := &gent.HistoryEntry{Origin: gent.RecordSystemInjected,
			Messages: []*gent.MessageContent{{Role: gent.ChatMessageTypeHuman,
				Parts: []gent.ContentPart{gent.TextPart(input.Marker)}}}}
		entry.SetMetadata(retentionNoticeKey, input.Marker)
		owned, err := execCtx.NewHistoryEntry(entry)
		if err != nil {
			return nil, err
		}
		entries = append(entries, owned)
	}
	return entries, nil
}

func (s *SummarizationStrategy) summaryGroups(input *gent.CompactionInput,
	users map[gent.EntryId]bool,
) (existing, summarize, keep []*gent.HistoryEntry) {
	required := map[gent.EntryId]bool{}
	for _, id := range input.Required {
		required[id] = true
	}
	var candidates []*gent.HistoryEntry
	for _, entry := range input.Entries {
		if users[entry.Id] {
			continue
		}
		if _, notice := entry.GetMetadata(retentionNoticeKey); notice {
			continue
		}
		if isPinned(entry) || required[entry.Id] {
			keep = append(keep, entry)
			continue
		}
		if entry.Origin == gent.RecordCompactedSynthetic {
			existing = append(existing, entry)
			continue
		}
		candidates = append(candidates, entry)
	}
	split := max(0, len(candidates)-s.keepRecent)
	keep = append(keep, candidates[split:]...)
	covered := map[gent.EntryId]bool{}
	for _, entry := range candidates[:split] {
		covered[entry.Id] = true
	}
	// Preserve original source order in the summary. User input is always covered before its
	// retained copy is shortened; KeepRecent and importance pins do not protect user messages.
	for _, entry := range input.Entries {
		if users[entry.Id] || covered[entry.Id] {
			summarize = append(summarize, entry)
		}
	}
	return existing, summarize, keep
}

// Text carries role and call attribution. Keep non-text context as native parts so the summary
// model can inspect covered media and structured values through its normal adapter contract.
// A provider that cannot consume a part uses its configured unsupported-media policy.
func summaryAttachments(entries []*gent.HistoryEntry) []*gent.ModelMessage {
	var messages []*gent.ModelMessage
	for _, entry := range entries {
		for index, message := range entry.Messages {
			var attachments []gent.ContentPart
			for _, part := range message.Parts {
				attachments = append(attachments, summaryNonText(part)...)
			}
			if len(attachments) == 0 {
				continue
			}
			label := fmt.Sprintf("Attached context from entry %q, message %d, role %s:",
				entry.Id, index, message.Role)
			messages = append(messages, &gent.ModelMessage{Role: gent.ChatMessageTypeHuman,
				Parts: append([]gent.ContentPart{gent.TextPart(label)}, attachments...)})
		}
	}
	return messages
}

func summaryNonText(part gent.ContentPart) []gent.ContentPart {
	switch part := part.(type) {
	case *gent.AssistantContent:
		var parts []gent.ContentPart
		for _, inner := range part.Content {
			parts = append(parts, summaryNonText(inner)...)
		}
		return parts
	case gent.TextContent, *gent.TextContent, gent.ModelToolCallRequest, *gent.ModelToolCallRequest:
		return nil
	case *gent.ModelToolCallResponse:
		var parts []gent.ContentPart
		for _, value := range part.Parts() {
			parts = append(parts, summaryNonText(value)...)
		}
		return parts
	case gent.CachedContent:
		if len(summaryNonText(part.ContentPart)) == 0 {
			return nil
		}
	case *gent.CachedContent:
		if len(summaryNonText(part.ContentPart)) == 0 {
			return nil
		}
	}
	return []gent.ContentPart{part}
}
