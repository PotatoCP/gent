package gent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type ownershipMetadata struct {
	Id     uint64
	Values []string
}

type ownershipSubscriber struct{ events []*RecordedEvent }

func (subscriber *ownershipSubscriber) OnEvent(_ context.Context, event *RecordedEvent) error {
	subscriber.events = append(subscriber.events, event)
	return nil
}
func (*ownershipSubscriber) OnStop(context.Context, *SubscriberStop) {}

func TestHistoryEntryOwnershipAndTypedReplay(t *testing.T) {
	type input struct{ source string }
	type expected struct {
		message  string
		metadata *ownershipMetadata
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	want := expected{"original policy", &ownershipMetadata{1<<63 + 19, []string{"original"}}}
	cases := []testCase{
		{"working context read", input{"scratchpad"}, want},
		{"recorder input", input{"recorder"}, want},
		{"subscriber input", input{"subscriber"}, want},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			registry := NewCodecRegistry()
			require.NoError(t, registry.RegisterType("test/ownership", 1, ownershipMetadata{}))
			recorder := &runStateRecorder{codecs: registry, capture: true}
			subscriber := &ownershipSubscriber{}
			var proposal *HistoryEntry
			loop := &turnTestLoop{next: func(execCtx *ExecutionContext) (*AgentLoopResult, error) {
				caller := &HistoryEntry{Messages: []*MessageContent{{Role: ChatMessageTypeAI,
					Parts: []ContentPart{TextPart("original policy")}}},
					Metadata: map[RecordMetadataKey]any{"custom": &ownershipMetadata{
						Id: 1<<63 + 19, Values: []string{"original"}}}}
				var err error
				proposal, err = execCtx.NewHistoryEntry(caller)
				if err != nil {
					return nil, err
				}
				draft := execCtx.ContextDraft()
				if err := execCtx.UpdateHistory(HistoryUpdate{ExpectedDraftRevision: draft.Revision,
					AppendedEntries: []*HistoryEntry{proposal},
					Active:          append(draft.Active, proposal.Id), Reason: ContextAppend}); err != nil {
					return nil, err
				}
				return acceptTurnText(execCtx, "Done")
			}}
			runner := NewRunner[string](loop, &Config{Codecs: registry, Recorder: recorder,
				Subscribers: []*SubscriberRegistration{{Subscriber: subscriber}}})
			execCtx, err := runner.NewExecutionContext(context.Background(), "ownership")
			require.NoError(t, err)
			defer execCtx.Release()
			started := time.Now()
			_, _, err = runner.NewTurn(execCtx, NewTask(TextPart("Start.")))
			require.NoError(t, err)
			view := NewRunView(execCtx.RunId())
			for _, data := range recorder.batches {
				var batch SaveBatch
				require.NoError(t, registry.Unmarshal(data, &batch))
				applied, err := view.Apply(&batch, registry)
				require.NoError(t, err)
				require.Equal(t, ApplyApplied, applied.Status)
			}
			entry, found := view.History.Entry(proposal.Id)
			require.True(t, found)
			assert.GreaterOrEqual(t, entry.CreatedAt.UnixNano(), started.UnixNano())
			assert.Equal(t, &HistoryEntry{Id: proposal.Id, Order: 2, RunId: execCtx.RunId(),
				ContextId: execCtx.ContextId(), AttemptId: execCtx.AttemptId(), Kind: HistoryMessages,
				Iteration: 1, CreatedAt: entry.CreatedAt, Messages: []*MessageContent{{Role: ChatMessageTypeAI,
					Parts: []ContentPart{TextPart(test.expected.message)}}},
				Metadata: map[RecordMetadataKey]any{"custom": test.expected.metadata}}, entry)
			var target *HistoryEntry
			if test.input.source == "scratchpad" {
				entries, err := execCtx.Scratchpad()
				require.NoError(t, err)
				for _, entry := range entries {
					if entry.Id == proposal.Id {
						target = entry
					}
				}
			} else {
				events := subscriber.events
				if test.input.source == "recorder" {
					events = savedTestEvents(t, recorder)
				}
				for _, event := range events {
					if change, ok := event.Payload.(*HistoryChange); ok {
						for _, entry := range change.AppendedEntries {
							if entry.Id == proposal.Id {
								target = entry
							}
						}
					}
				}
			}
			require.NotNil(t, target)
			before, err := registry.Marshal(view)
			require.NoError(t, err)
			// Consumer values are read-only. An editor must make its own independent copy,
			// preserving concrete values and exact integer data across that copy.
			target, err = cloneOwned(registry, target)
			require.NoError(t, err)
			target.Messages[0].Parts[0] = TextPart("changed")
			target.Metadata["custom"].(*ownershipMetadata).Values[0] = "changed"
			after, err := registry.Marshal(view)
			require.NoError(t, err)
			assert.Equal(t, before, after)
			fresh := NewCodecRegistry()
			require.NoError(t, fresh.RegisterType("test/ownership", 1, ownershipMetadata{}))
			var loaded RunView
			require.NoError(t, fresh.Unmarshal(before, &loaded))
			assert.Equal(t, *view, loaded)
			full := NewRunState(execCtx.RunId())
			for _, batch := range recorder.batches {
				applyRecordedState(t, full, fresh, batch)
			}
			assert.Equal(t, encodeRunState(t, fresh, full),
				encodeRunState(t, registry, recorder.states[len(recorder.states)-1]))
		})
	}
}

func TestHistoryRejectsNilPointers(t *testing.T) {
	type input struct{ kind string }
	type expected struct{ errorText string }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"constructor", input{"constructor"}, expected{"gent: nil history entry"}},
		{"update", input{"update"}, expected{"gent: nil history entry"}},
		{"message", input{"message"}, expected{"gent: nil history message"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			execCtx := newComponentContext(t, &Config{})
			before, draft := execCtx.History(), execCtx.ContextDraft()
			var err error
			switch test.input.kind {
			case "constructor":
				_, err = execCtx.NewHistoryEntry(nil)
			case "update":
				err = execCtx.UpdateHistory(HistoryUpdate{AppendedEntries: []*HistoryEntry{nil}})
			case "message":
				entry, makeErr := execCtx.NewHistoryEntry(&HistoryEntry{Messages: []*MessageContent{nil}})
				require.NoError(t, makeErr)
				err = execCtx.UpdateHistory(HistoryUpdate{AppendedEntries: []*HistoryEntry{entry},
					Active: []EntryId{entry.Id}})
			}
			assert.EqualError(t, err, test.expected.errorText)
			assert.Equal(t, before, execCtx.History())
			assert.Equal(t, draft, execCtx.ContextDraft())
		})
	}
}
