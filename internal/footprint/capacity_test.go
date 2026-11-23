package footprint_test

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"testing"

	"github.com/rickchristie/gent"
)

type waitingModel struct{ entered chan struct{} }

func (*waitingModel) PrepareRequest(execCtx *gent.ExecutionContext,
	request *gent.ModelCallRequest, chain gent.ToolChain,
) (*gent.ModelCallRequest, error) {
	return gent.PrepareModelRequest(execCtx, request, chain)
}

func (model *waitingModel) GenerateContentStream(
	ctx context.Context, _ *gent.ModelCallRequest,
) (gent.Stream, error) {
	stream, err := gent.NewBoundedStreamWithDuration(ctx, 8, nil)
	if err != nil {
		return nil, err
	}
	go func() {
		model.entered <- struct{}{}
		<-ctx.Done()
		stream.Complete(nil, ctx.Err())
	}()
	return stream, nil
}

// A remote store would retain these bytes outside the worker. Include normal encoding in the
// worker cost, but discard the acknowledged bytes here. This benchmark does not measure a
// database, HTTP transport, or provider latency. Full stored replay has a separate benchmark.
type deliveryRecorder struct{ codecs *gent.CodecRegistry }

func (recorder *deliveryRecorder) Save(_ context.Context, batch *gent.SaveBatch,
	_ func() (*gent.RunState, error),
) error {
	_, err := recorder.codecs.Marshal(batch)
	return err
}

// Each run has a distinct identity, one completed business turn, and a restored next turn
// waiting on its provider stream. Cancellation must join all owned work. This is a local
// framework memory test, not proof of t3.medium CPU, network, or provider capacity.
func BenchmarkWaitingBusinessRuns(b *testing.B) {
	for _, count := range []int{1000, 3000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			for b.Loop() {
				measureWaitingBusinessRuns(b, count)
			}
		})
	}
}

func measureWaitingBusinessRuns(b *testing.B, count int) {
	b.Helper()
	codecs := gent.NewCodecRegistry()
	model := &waitingModel{entered: make(chan struct{}, count)}
	runner := configureBusiness(model, codecs, &deliveryRecorder{codecs: codecs})
	ctx, cancel := context.WithCancel(context.Background())
	contexts := make([]*gent.ExecutionContext, 0, count)
	defer func() {
		cancel()
		for _, execCtx := range contexts {
			execCtx.Release()
		}
	}()
	finished := make(chan error, count)
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	workers := runtime.NumGoroutine()
	for range count {
		_, recorder := runBusiness(b, 1)
		state := loadBusinessState(b, recorder)
		restored, err := runner.Restore(ctx, state)
		if err != nil || restored.Status != gent.RestoreReady {
			b.Fatalf("restore: result %v, error %v", restored, err)
		}
		contexts = append(contexts, restored.Context)
		go func(execCtx *gent.ExecutionContext) {
			_, result, err := runner.NewTurn(execCtx,
				gent.NewTask(gent.TextPart("Check the updated delivery facts.")))
			if result != nil || !errors.Is(err, context.Canceled) {
				finished <- fmt.Errorf("canceled execution: result %v, error %w", result, err)
				return
			}
			finished <- nil
		}(restored.Context)
		<-model.entered
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	heap := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	stack := int64(after.StackInuse) - int64(before.StackInuse)
	b.ReportMetric(float64(heap)/float64(count), "heap-B/run")
	b.ReportMetric(float64(stack)/float64(count), "stack-B/run")
	b.ReportMetric(float64(runtime.NumGoroutine()-workers)/float64(count), "goroutines/run")
	b.ReportMetric(float64(count), "waiting-runs")
	cancel()
	for range count {
		if err := <-finished; err != nil {
			b.Fatal(err)
		}
	}
	for _, execCtx := range contexts {
		execCtx.Release()
	}
	b.ReportMetric(float64(runtime.NumGoroutine()-workers), "remaining-workers")
	runtime.KeepAlive(contexts)
}
