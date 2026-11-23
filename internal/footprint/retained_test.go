package footprint_test

import (
	"runtime"
	"testing"

	"github.com/rickchristie/gent"
)

// Measure retained values after decoding, without counting the source documents twice. The
// complete archive and compact execution state have different purposes and memory costs.
func BenchmarkRetainedBusinessData(b *testing.B) {
	runID, recorder := runBusiness(b, 10)
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
	archive, err := recorder.codecs.Marshal(view)
	if err != nil {
		b.Fatal(err)
	}
	type input struct {
		name string
		data []byte
		load func() any
	}
	for _, item := range []input{
		{"state", recorder.snapshot, func() any {
			loaded, err := recorder.codecs.UnmarshalRunState(recorder.snapshot)
			if err != nil || loaded.Status != gent.RunStateReady {
				b.Fatalf("RunStateSnapshot: result %v, error %v", loaded, err)
			}
			return loaded.State
		}},
		{"archive", archive, func() any {
			var restored *gent.RunView
			if err := recorder.codecs.Unmarshal(archive, &restored); err != nil {
				b.Fatal(err)
			}
			return restored
		}},
	} {
		b.Run(item.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				b.ReportMetric(float64(measureRetained(item.load)), "live-B")
			}
			b.ReportMetric(float64(len(item.data)), "stored-B")
		})
	}
	runtime.KeepAlive(view)
}

func measureRetained(load func() any) int64 {
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	value := load()
	runtime.GC()
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(value)
	return int64(after.HeapAlloc) - int64(before.HeapAlloc)
}
