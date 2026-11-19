package compaction

import (
	"github.com/rickchristie/gent"
	"github.com/stretchr/testify/assert"
	"testing"
)

func testShouldCompact(
	t *testing.T,
	trigger gent.CompactionTrigger,
	execCtx *gent.ExecutionContext,
) bool {
	t.Helper()
	result, err := trigger.ShouldCompact(execCtx)
	assert.NoError(t, err)
	return result
}

func setTestRecords(t testing.TB, execCtx *gent.ExecutionContext, records []*gent.HistoryEntry) {
	t.Helper()
	draft := execCtx.ContextDraft()
	var entries []*gent.HistoryEntry
	var active []gent.EntryId
	for _, record := range records {
		entry, err := execCtx.NewHistoryEntry(record)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, entry)
		active = append(active, entry.Id)
	}
	if err := execCtx.UpdateHistory(
		gent.HistoryUpdate{
			ExpectedDraftRevision: draft.Revision,
			AppendedEntries:       entries,
			Active:                active,
			Reason:                gent.ContextReplace,
		},
	); err != nil {
		t.Fatal(err)
	}
}
func testScratchpad(t testing.TB, execCtx *gent.ExecutionContext) []*gent.HistoryEntry {
	t.Helper()
	records, err := execCtx.Scratchpad()
	if err != nil {
		t.Fatal(err)
	}
	result := make([]*gent.HistoryEntry, len(records))
	for i := range records {
		result[i] = recordContent(records[i])
	}
	return result
}

// These prompt/expiry tests compare complete message content. Durable identity and
// ownership are tested separately; strip execution-assigned fields from these owned snapshots.
func recordContent(entry *gent.HistoryEntry) *gent.HistoryEntry {
	return &gent.HistoryEntry{Messages: entry.Messages, Origin: entry.Origin,
		ExpireAfterIteration: entry.ExpireAfterIteration, Metadata: entry.Metadata}
}

func applyTestCompaction(execCtx *gent.ExecutionContext, strategy gent.CompactionStrategy) error {
	input, err := execCtx.CompactionInput()
	if err != nil {
		return err
	}
	proposal, err := strategy.Compact(execCtx, input)
	if err != nil {
		return err
	}
	proposal.Reason = gent.ContextCompact
	return execCtx.UpdateHistory(*proposal)
}
