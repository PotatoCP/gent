package gent

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

type stateBenchmarkStore struct {
	codecs  *CodecRegistry
	batches [][]byte
	states  [][]byte
	capture func(func() (*RunState, error))
}

func (store *stateBenchmarkStore) Save(_ context.Context, batch *SaveBatch,
	capture func() (*RunState, error),
) error {
	encoded, err := store.codecs.Marshal(batch)
	if err != nil {
		return err
	}
	state, err := capture()
	if err != nil {
		return err
	}
	data, err := store.codecs.MarshalRunState(state)
	if err != nil {
		return err
	}
	store.batches = append(store.batches, encoded)
	store.states = append(store.states, data)
	if batch.Checkpoint.Phase == PhasePaused {
		store.capture(capture)
	}
	return nil
}

// Each case makes a real accepted log. The active context stays at one entry. The large-state
// case measures retained payload cost separately from log length. Capture runs inside Save so it
// measures the public lazy capture contract, not a copy of an unrelated test representation.
func BenchmarkRunStateStorage(b *testing.B) {
	type input struct{ iterations, contentBytes int }
	cases := []struct {
		name  string
		input input
	}{
		{"100 iterations", input{100, 128}},
		{"2000 iterations", input{2000, 128}},
		{"1 MiB active", input{2, 1 << 20}},
	}
	for _, test := range cases {
		b.Run(test.name, func(b *testing.B) {
			codecs := NewCodecRegistry()
			store := &stateBenchmarkStore{codecs: codecs, capture: func(capture func() (*RunState, error)) {
				b.Run("capture", func(b *testing.B) {
					b.ReportAllocs()
					for b.Loop() {
						if _, err := capture(); err != nil {
							b.Fatal(err)
						}
					}
				})
			}}
			content := strings.Repeat("x", test.input.contentBytes)
			loop := &turnTestLoop{next: func(execCtx *ExecutionContext) (*AgentLoopResult, error) {
				entry, err := execCtx.NewHistoryEntry(&HistoryEntry{Messages: []*MessageContent{
					{Role: ChatMessageTypeHuman, Parts: []ContentPart{TextPart(content)}},
				}})
				if err != nil {
					return nil, err
				}
				draft := execCtx.ContextDraft()
				if err := execCtx.UpdateHistory(HistoryUpdate{ExpectedDraftRevision: draft.Revision,
					AppendedEntries: []*HistoryEntry{entry}, Active: []EntryId{entry.Id},
					Reason: ContextReplace}); err != nil {
					return nil, err
				}
				return &AgentLoopResult{Action: LAContinue}, nil
			}}
			runner := NewRunner[string](loop, &Config{Codecs: codecs, Recorder: store,
				Limits: []Limit{{Type: LimitExactKey, Key: SCIterations,
					MaxValue: float64(test.input.iterations)}}})
			execCtx, err := runner.NewExecutionContext(context.Background(), "storage benchmark")
			if err != nil {
				b.Fatal(err)
			}
			_, result, err := runner.NewTurn(execCtx, NewTask(TextPart("Update the active record.")))
			execCtx.Release()
			if err != nil || result.Status != ExecutionPaused {
				b.Fatalf("prepare accepted log: result %v, error %v", result, err)
			}
			last := len(store.states) - 1
			loaded, err := codecs.UnmarshalRunState(store.states[last])
			if err != nil || loaded.Status != RunStateReady {
				b.Fatalf("load benchmark state: result %v, error %v", loaded, err)
			}
			b.Run("encode", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if _, err := codecs.MarshalRunState(loaded.State); err != nil {
						b.Fatal(err)
					}
				}
				b.ReportMetric(float64(len(store.states[last])), "saved-B")
			})
			b.Run("load", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					result, err := codecs.UnmarshalRunState(store.states[last])
					if err != nil || result.Status != RunStateReady {
						b.Fatalf("load: result %v, error %v", result, err)
					}
				}
				b.ReportMetric(float64(len(store.states[last])), "read-B")
			})
			b.Run("load and tail", func(b *testing.B) {
				b.ReportAllocs()
				start := max(0, last-4)
				readBytes := len(store.states[start])
				for _, data := range store.batches[start+1:] {
					readBytes += len(data)
				}
				for b.Loop() {
					result, err := codecs.UnmarshalRunState(store.states[start])
					if err != nil || result.Status != RunStateReady {
						b.Fatalf("load: result %v, error %v", result, err)
					}
					benchmarkApply(b, codecs, store.batches[start+1:], result.State.Apply)
				}
				b.ReportMetric(float64(readBytes), "read-B")
			})
			for _, archive := range []bool{false, true} {
				b.Run(fmt.Sprintf("full replay archive=%v", archive), func(b *testing.B) {
					b.ReportAllocs()
					readBytes := 0
					for _, data := range store.batches {
						readBytes += len(data)
					}
					for b.Loop() {
						apply := NewRunState(execCtx.RunId()).Apply
						if archive {
							apply = NewRunView(execCtx.RunId()).Apply
						}
						benchmarkApply(b, codecs, store.batches, apply)
					}
					b.ReportMetric(float64(readBytes), "read-B")
				})
			}
		})
	}
}

func benchmarkApply(b *testing.B, codecs *CodecRegistry, batches [][]byte,
	apply func(*SaveBatch, *CodecRegistry) (*ApplyStateResult, error),
) {
	b.Helper()
	for _, data := range batches {
		var batch SaveBatch
		if err := codecs.Unmarshal(data, &batch); err != nil {
			b.Fatal(err)
		}
		result, err := apply(&batch, codecs)
		if err != nil || result.Status != ApplyApplied {
			b.Fatalf("apply: result %v, error %v", result, err)
		}
	}
}
