package gent

import (
	"context"
	"runtime"
	"testing"
)

// These handles have no task yet. Measure this fixed cost separately from active context,
// archives, model traffic, and application storage; it is not a service capacity claim.
func BenchmarkIdleConversations(b *testing.B) {
	loop := &turnTestLoop{}
	runner := NewRunner[string](loop, &Config{})
	for b.Loop() {
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		contexts := make([]*ExecutionContext, 1000)
		for index := range contexts {
			var err error
			contexts[index], err = runner.NewExecutionContext(context.Background(), "waiting")
			if err != nil {
				b.Fatal(err)
			}
		}
		runtime.GC()
		runtime.ReadMemStats(&after)
		b.ReportMetric(float64(int64(after.HeapAlloc)-int64(before.HeapAlloc))/1000, "live-B/run")
		for _, execCtx := range contexts {
			execCtx.Release()
		}
		runtime.KeepAlive(contexts)
	}
	b.ReportAllocs()
}
