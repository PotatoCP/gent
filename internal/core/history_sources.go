package gent

import "fmt"

// historySource proves that a direct archive reference was checked at acceptance. It retains only
// the source identity's context and absolute order, not its content or its own source references.
// A compact state must not retain the full chain of earlier summaries and replacements.
type historySource struct {
	ContextId string
	Order     uint64
}

func sourceForEntry(entry *HistoryEntry) historySource {
	return historySource{ContextId: entry.ContextId, Order: entry.Order}
}

func retainedHistorySources(previous map[EntryId]historySource, before, retained HistorySnapshot,
) map[EntryId]historySource {
	available := make(map[EntryId]*HistoryEntry, len(before.Entries))
	selected := make(map[EntryId]bool, len(retained.Entries))
	for _, entry := range before.Entries {
		available[entry.Id] = entry
	}
	for _, entry := range retained.Entries {
		selected[entry.Id] = true
	}
	var sources map[EntryId]historySource
	for _, entry := range retained.Entries {
		for _, id := range entry.Replaces {
			if selected[id] {
				continue
			}
			if sources == nil {
				sources = map[EntryId]historySource{}
			}
			source := previous[id]
			if original := available[id]; original != nil {
				source = sourceForEntry(original)
			}
			sources[id] = source
		}
	}
	return sources
}

func validateHistorySources(data *runStateData) error {
	entries := make(map[EntryId]*HistoryEntry, len(data.History.Entries))
	used := make(map[EntryId]bool, len(data.HistorySources))
	for _, entry := range data.History.Entries {
		entries[entry.Id] = entry
	}
	for _, entry := range data.History.Entries {
		for _, id := range entry.Replaces {
			source, found := data.HistorySources[id]
			if original := entries[id]; original != nil {
				source, found = sourceForEntry(original), true
			} else {
				used[id] = true
			}
			if !found || id == "" || source.ContextId != entry.ContextId || source.Order == 0 ||
				source.Order >= entry.Order {
				return fmt.Errorf("gent: invalid retained archive source %q for %q", id, entry.Id)
			}
		}
	}
	if len(used) != len(data.HistorySources) {
		return fmt.Errorf("gent: retained archive sources do not match direct references")
	}
	for id := range data.HistorySources {
		if !used[id] {
			return fmt.Errorf("gent: unused retained archive source %q", id)
		}
	}
	return nil
}
