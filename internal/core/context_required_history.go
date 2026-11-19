package gent

const requiredHistoryState = "gent:required_history"

// AppendRequiredRecord selects a complete conversation group until the loop releases it. This
// keeps a rejected candidate and its steering together through automatic or custom compaction.
// The entry and requirement enter the same history update and the same checkpoint.
func (execCtx *ExecutionContext) AppendRequiredRecord(record *HistoryEntry) error {
	entry, err := execCtx.NewHistoryEntry(record)
	if err != nil {
		return err
	}
	// An append needs IDs and a revision, not copies of all prior images. Capture these together
	// and let UpdateHistory reject a concurrent change to the selected history.
	execCtx.run.mu.RLock()
	revision := execCtx.draftRevision
	active := append([]EntryId(nil), execCtx.active...)
	required, err := execCtx.requiredHistoryLocked()
	execCtx.run.mu.RUnlock()
	if err != nil {
		return err
	}
	required = append(required, entry.Id)
	encoded, err := execCtx.Codecs().Encode(required)
	if err != nil {
		return err
	}
	return execCtx.UpdateHistory(HistoryUpdate{
		ExpectedDraftRevision: revision, AppendedEntries: []*HistoryEntry{entry},
		Active: append(active, entry.Id), Reason: ContextAppend,
		CustomState: []StateValue{{ContextId: execCtx.ContextId(), Name: requiredHistoryState,
			Value: encoded}},
	})
}

// ReleaseRequiredRecords permits compaction after the main loop has consumed the selected
// groups. Save this change with the response that consumed them, not with a compaction model call.
func (execCtx *ExecutionContext) ReleaseRequiredRecords() {
	execCtx.ClearCheckpointState(requiredHistoryState)
}

func (execCtx *ExecutionContext) requiredHistoryLocked() ([]EntryId, error) {
	value, ok := execCtx.state[requiredHistoryState]
	if !ok {
		return nil, nil
	}
	return decodeAs[[]EntryId](execCtx.Codecs(), value)
}

func keepRequiredRecords(active, required []EntryId) []EntryId {
	if len(required) == 0 {
		return active
	}
	selected := make(map[EntryId]bool, len(active))
	for _, id := range active {
		selected[id] = true
	}
	for _, id := range required {
		if !selected[id] {
			active = append(active, id)
			selected[id] = true
		}
	}
	return active
}
