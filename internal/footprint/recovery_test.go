package footprint_test

import (
	"context"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/stretchr/testify/assert"
)

func loadBusinessState(t testing.TB, recorder *businessRecorder) *gent.RunState {
	t.Helper()
	loaded, err := recorder.codecs.UnmarshalRunState(recorder.snapshot)
	if err != nil || loaded.Status != gent.RunStateReady {
		t.Fatalf("RunStateSnapshot: result %v, error %v", loaded, err)
	}
	applyBusinessTail(t, recorder, loaded.State, recorder.tail)
	return loaded.State
}

func applyBusinessTail(t testing.TB, recorder *businessRecorder, state *gent.RunState, from int) {
	t.Helper()
	for _, data := range recorder.batches[from:] {
		var batch gent.SaveBatch
		if err := recorder.codecs.Unmarshal(data, &batch); err != nil {
			t.Fatal(err)
		}
		result, err := state.Apply(&batch, recorder.codecs)
		if err != nil || result.Status != gent.ApplyApplied {
			t.Fatalf("replay: result %v, error %v", result, err)
		}
	}
}

func TestBusinessRecovery(t *testing.T) {
	type input struct{ compression gent.CodecCompression }
	type expected struct{ status gent.ExecutionStatus }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"plain", input{gent.CodecCompressionNone}, expected{gent.ExecutionFinished}},
		{"compressed", input{gent.CodecCompressionSnappy}, expected{gent.ExecutionFinished}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			runID, recorder := runBusiness(t, 3, gent.WithCodecCompression(test.input.compression))
			full := gent.NewRunState(runID)
			applyBusinessTail(t, recorder, full, 0)
			state := loadBusinessState(t, recorder)
			want, err := recorder.codecs.MarshalRunState(full)
			assert.NoError(t, err)
			actual, err := recorder.codecs.MarshalRunState(state)
			assert.NoError(t, err)
			assert.Equal(t, want, actual)
			runner := businessRunner(recorder)
			restored, err := runner.Restore(context.Background(), state)
			if !assert.NoError(t, err) || !assert.Equal(t, gent.RestoreReady, restored.Status) {
				return
			}
			defer restored.Context.Release()
			previous, _, err := runner.GetLastTurnResult(restored.Context)
			assert.NoError(t, err)
			answer, result, err := runner.NewTurn(restored.Context,
				gent.NewTask(gent.TextPart("Check the delivery again.")))
			if !assert.NoError(t, err) {
				return
			}
			assert.Equal(t, test.expected, expected{result.Status})
			assert.Equal(t, previous, answer)
			assert.Equal(t, runID, restored.Context.RunId())
		})
	}
}

func BenchmarkBusinessReplay(b *testing.B) {
	runID, recorder := runBusiness(b, 10)
	b.Run("full", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			state := gent.NewRunState(runID)
			applyBusinessTail(b, recorder, state, 0)
		}
	})
	b.Run("snapshot_tail_restore", func(b *testing.B) {
		runner := businessRunner(recorder)
		b.ReportAllocs()
		for b.Loop() {
			state := loadBusinessState(b, recorder)
			restored, err := runner.Restore(context.Background(), state)
			if err != nil || restored.Status != gent.RestoreReady {
				b.Fatalf("restore: result %v, error %v", restored, err)
			}
			restored.Context.Release()
		}
	})
	b.Run("archive", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			view := gent.NewRunView(runID)
			for _, data := range recorder.batches {
				var batch gent.SaveBatch
				if err := recorder.codecs.Unmarshal(data, &batch); err != nil {
					b.Fatal(err)
				}
				result, err := view.Apply(&batch, recorder.codecs)
				if err != nil || result.Status != gent.ApplyApplied {
					b.Fatalf("archive: result %v, error %v", result, err)
				}
			}
		}
	})
}
