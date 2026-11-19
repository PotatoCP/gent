package compaction

import (
	"fmt"
	"slices"
	"unicode/utf8"

	"github.com/rickchristie/gent"
)

const defaultUserRetentionMarker = "[Earlier user content was omitted during compaction.]"
const defaultUserRetentionBytes = 32 * 1024

type boundaryUserRetention struct{}

func (boundaryUserRetention) Retain(input *gent.UserMessageRetentionInput) (
	*gent.UserMessageRetentionResult, error,
) {
	if input.MaxBytes < len(input.Marker) || input.Marker == "" {
		return nil, fmt.Errorf("gent: user retention allowance %d must fit the complete %d-byte notice",
			input.MaxBytes, len(input.Marker))
	}
	if !utf8.ValidString(input.Marker) {
		return nil, fmt.Errorf("gent: user retention notice is not valid UTF-8")
	}
	remaining := input.MaxBytes
	omitted := input.EarlierContentOmitted
	for _, entry := range input.Messages {
		omitted = omitted || entry.Truncated
		for _, part := range entry.Message.Parts {
			size, err := retentionPartBytes(part)
			if err != nil {
				return nil, err
			}
			if size > remaining {
				omitted = true
				remaining = 0
			} else {
				remaining -= size
			}
		}
	}
	if !omitted {
		return &gent.UserMessageRetentionResult{Messages: input.Messages,
			RetainedBytes: input.MaxBytes - remaining}, nil
	}
	remaining = input.MaxBytes - len(input.Marker)
	var retained []*gent.UserMessageRetentionEntry
	for i := len(input.Messages) - 1; i >= 0; i-- {
		entry := input.Messages[i]
		parts, used, clipped, err := retainMessageSuffix(entry.Message.Parts, remaining)
		if err != nil {
			return nil, err
		}
		if len(parts) != 0 {
			selected := entry
			if clipped {
				projection := *entry.Message
				projection.Parts = parts
				selected = &gent.UserMessageRetentionEntry{EntryId: entry.EntryId,
					MessageIndex: entry.MessageIndex, Message: &projection, Truncated: true}
			}
			retained = append(retained, selected)
			remaining -= used
		}
		if clipped || remaining == 0 {
			break
		}
	}
	slices.Reverse(retained)
	return &gent.UserMessageRetentionResult{Messages: retained,
		RetainedBytes: input.MaxBytes - remaining, EarlierContentOmitted: true}, nil
}

func retainMessageSuffix(parts []gent.ContentPart, allowance int) (
	[]gent.ContentPart, int, bool, error,
) {
	remaining := allowance
	var suffix []gent.ContentPart
	for i := len(parts) - 1; i >= 0; i-- {
		size, err := retentionPartBytes(parts[i])
		if err != nil {
			return nil, 0, false, err
		}
		if size <= remaining {
			suffix = append(suffix, parts[i])
			remaining -= size
			continue
		}
		part, used := retainTextSuffix(parts[i], remaining)
		if part != nil {
			suffix = append(suffix, part)
			remaining -= used
		}
		slices.Reverse(suffix)
		return suffix, allowance - remaining, true, nil
	}
	// An unchanged message shares its immutable parts and source identity.
	return parts, allowance - remaining, false, nil
}

func retentionPartBytes(part gent.ContentPart) (int, error) {
	switch part := part.(type) {
	case gent.TextContent:
		if !utf8.ValidString(part.Text) {
			return 0, fmt.Errorf("gent: user text is not valid UTF-8")
		}
		return len(part.Text), nil
	case *gent.TextContent:
		return retentionPartBytes(*part)
	case gent.BinaryContent:
		return len(part.Data), nil
	case *gent.BinaryContent:
		return len(part.Data), nil
	case gent.ImageURLContent:
		return len(part.URL), nil
	case *gent.ImageURLContent:
		return len(part.URL), nil
	case gent.DataContent:
		return len(part.String()), nil
	case *gent.DataContent:
		return len(part.String()), nil
	case gent.CachedContent:
		return retentionPartBytes(part.ContentPart)
	case *gent.CachedContent:
		return retentionPartBytes(part.ContentPart)
	default:
		return 0, fmt.Errorf("gent: unsupported user retention part %T", part)
	}
}

func retainTextSuffix(part gent.ContentPart, allowance int) (gent.ContentPart, int) {
	switch part := part.(type) {
	case gent.TextContent:
		start := len(part.Text) - allowance
		for start < len(part.Text) && !utf8.RuneStart(part.Text[start]) {
			start++
		}
		if start == len(part.Text) {
			return nil, 0
		}
		return gent.TextPart(part.Text[start:]), len(part.Text) - start
	case *gent.TextContent:
		return retainTextSuffix(*part, allowance)
	case gent.CachedContent:
		content, size := retainTextSuffix(part.ContentPart, allowance)
		if content == nil {
			return nil, 0
		}
		return gent.CachedContent{ContentPart: content, CacheControl: part.CacheControl}, size
	case *gent.CachedContent:
		return retainTextSuffix(*part, allowance)
	default:
		return nil, 0
	}
}
