package footprint_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/agents/react"
	"github.com/rickchristie/gent/executor"
	"github.com/rickchristie/gent/toolchain"
)

type rateCompletion struct {
	rate    int
	seconds float64
	err     error
}

type rateGate struct {
	ready   chan struct{}
	planned time.Time
	rate    int
}
type rateModel struct{ entered chan *rateGate }

func (*rateModel) PrepareRequest(execCtx *gent.ExecutionContext,
	request *gent.ModelCallRequest, chain gent.ToolChain,
) (*gent.ModelCallRequest, error) {
	return gent.PrepareModelRequest(execCtx, request, chain)
}
func (model *rateModel) GenerateContentStream(ctx context.Context,
	request *gent.ModelCallRequest,
) (gent.Stream, error) {
	if request.Messages[len(request.Messages)-1].Role == gent.ChatMessageTypeTool {
		return (&businessModel{}).GenerateContentStream(ctx, request)
	}
	stream, err := gent.NewBoundedStreamWithDuration(ctx, 8, nil)
	if err != nil {
		return nil, err
	}
	gate := &rateGate{ready: make(chan struct{})}
	go func() {
		model.entered <- gate
		select {
		case <-ctx.Done():
			stream.Complete(nil, ctx.Err())
		case <-gate.ready:
			source, err := (&businessModel{}).GenerateContentStream(ctx, request)
			if err != nil {
				stream.Complete(nil, err)
				return
			}
			response, err := source.Response()
			source.Close()
			stream.Complete(response, err)
		}
	}()
	return stream, nil
}

type rateRecorder struct {
	codecs                                   *gent.CodecRegistry
	counts                                   sync.Map
	batches, bytes, snapshots, snapshotBytes atomic.Int64
}

func (recorder *rateRecorder) Save(_ context.Context, batch *gent.SaveBatch,
	capture func() (*gent.RunState, error),
) error {
	data, err := recorder.codecs.Marshal(batch)
	if err != nil {
		return err
	}
	recorder.batches.Add(1)
	recorder.bytes.Add(int64(len(data)))
	counter, _ := recorder.counts.LoadOrStore(batch.RunId, &atomic.Int64{})
	if counter.(*atomic.Int64).Add(1)%4 != 1 {
		return nil
	}
	state, err := capture()
	if err != nil {
		return err
	}
	data, err = recorder.codecs.MarshalRunState(state)
	if err != nil {
		return err
	}
	recorder.snapshots.Add(1)
	recorder.snapshotBytes.Add(int64(len(data)))
	return nil
}

type rateSubscriber struct{ events, failures atomic.Int64 }

func (subscriber *rateSubscriber) OnEvent(context.Context, *gent.RecordedEvent) error {
	subscriber.events.Add(1)
	return nil
}
func (subscriber *rateSubscriber) OnStop(_ context.Context, result *gent.SubscriberStop) {
	if result.Reason != gent.SubscriberClosed || result.Error != nil {
		subscriber.failures.Add(1)
	}
}
func cpuSeconds() float64 {
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		panic(err)
	}
	return float64(usage.Utime.Sec+usage.Stime.Sec) +
		float64(usage.Utime.Usec+usage.Stime.Usec)/1e6
}
func envNumber(name string, fallback int) int {
	if value := os.Getenv(name); value != "" {
		number, err := strconv.Atoi(value)
		if err != nil {
			panic(err)
		}
		return number
	}
	return fallback
}

// All conversations restore real accepted state. Their next turns wait on managed streams.
// Releasing model replies at fixed times measures completion work among resident conversations.
// Remote model latency, network transport, and database storage are outside this workload.
// The host controller sets each CPU quota before it opens GENT_BENCH_GATE or its .burst file.
// Without that controller, the group names identify phases only; they do not enforce a CPU limit.
func BenchmarkResidentTurnRate(b *testing.B) {
	count, seconds := envNumber("GENT_BENCH_RUNS", 3000), envNumber("GENT_BENCH_SECONDS", 20)
	for b.Loop() {
		measureTurnRate(b, count, seconds)
	}
}
func measureTurnRate(b *testing.B, count, seconds int) {
	codecs := gent.NewCodecRegistry()
	recorder := &rateRecorder{codecs: codecs}
	subscriber := &rateSubscriber{}
	model := &rateModel{entered: make(chan *rateGate, count)}
	chain := toolchain.New()
	chain.RegisterTool(gent.DefineTool("lookup",
		func(_ context.Context, input *businessInput) (*gent.ToolExecutionResponse, error) {
			return &gent.ToolExecutionResponse{Status: gent.ToolExecuted,
				Parts: []gent.ContentPart{gent.TextPart(businessOrder(input.ID))}}, nil
		}).Description("Look up purchase order facts.").MustBuild())
	runner := executor.NewRunner(react.NewAgent(model), &gent.Config{
		Codecs: codecs, Recorder: recorder, ToolChain: chain,
		Subscribers: []*gent.SubscriberRegistration{
			{Subscriber: subscriber, Delivery: gent.SubscriberLive},
			{Subscriber: subscriber, Delivery: gent.SubscriberAccepted},
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	contexts := make([]*gent.ExecutionContext, 0, count)
	defer func() {
		cancel()
		for _, execCtx := range contexts {
			execCtx.Release()
		}
	}()
	finished := make(chan rateCompletion, count)
	gates := make([]*rateGate, count)
	runtime.GC()
	var before, ready runtime.MemStats
	runtime.ReadMemStats(&before)
	workers := runtime.NumGoroutine()
	for index := range count {
		_, saved := runBusiness(b, 1)
		state := loadBusinessState(b, saved)
		restored, err := runner.Restore(ctx, state)
		if err != nil || restored.Status != gent.RestoreReady {
			b.Fatalf("restore: %v %v", restored, err)
		}
		contexts = append(contexts, restored.Context)
		go func(index int, execCtx *gent.ExecutionContext) {
			answer, result, err := runner.NewTurn(execCtx,
				gent.NewTask(gent.TextPart("Check the updated delivery facts.")))
			gate := gates[index]
			if gate.planned.IsZero() {
				if result != nil || !errors.Is(err, context.Canceled) {
					finished <- rateCompletion{err: fmt.Errorf("cancel: result %v error %w", result, err)}
				} else {
					finished <- rateCompletion{}
				}
				return
			}
			elapsed := time.Since(gate.planned).Seconds()
			if err == nil && (result.Status != gent.ExecutionFinished || answer != businessAnswer()) {
				err = fmt.Errorf("unexpected completed result: %v", result)
			}
			finished <- rateCompletion{gate.rate, elapsed, err}
		}(index, restored.Context)
		gates[index] = <-model.entered
	}
	runtime.GC()
	runtime.ReadMemStats(&ready)
	readyMetrics := map[string]any{"runs": count,
		"heap_bytes":  int64(ready.HeapAlloc) - int64(before.HeapAlloc),
		"stack_bytes": int64(ready.StackInuse) - int64(before.StackInuse),
		"goroutines":  runtime.NumGoroutine() - workers}
	data, err := json.Marshal(readyMetrics)
	if err != nil {
		b.Fatal(err)
	}
	fmt.Println("LOAD_READY", string(data))
	waitRateQuota(b, "", data)
	released, sustained := measureReleaseGroup(b, "sustained_0.4_vcpu", gates, 0, seconds,
		[]int{1, 4, 8, 16}, finished, recorder, subscriber)
	waitRateQuota(b, ".burst", []byte("Ready for both vCPUs"))
	released, burst := measureReleaseGroup(b, "burst_2_vcpu", gates, released, seconds,
		[]int{4, 8, 16, 32}, finished, recorder, subscriber)
	result := map[string]any{"ready": readyMetrics, "completed": released,
		"groups": []map[string]any{sustained, burst}}
	cancel()
	for range count - released {
		if result := <-finished; result.err != nil {
			b.Fatal(result.err)
		}
	}
	for _, execCtx := range contexts {
		execCtx.Release()
	}
	result["subscriber_failures"] = subscriber.failures.Load()
	result["remaining_workers"] = runtime.NumGoroutine() - workers
	if subscriber.failures.Load() != 0 {
		b.Fatalf("subscriber failures: %d", subscriber.failures.Load())
	}
	if runtime.NumGoroutine() != workers {
		b.Fatalf("remaining workers: %d", runtime.NumGoroutine()-workers)
	}
	data, err = json.Marshal(result)
	if err != nil {
		b.Fatal(err)
	}
	fmt.Println("LOAD_RESULT", string(data))
}

// Each group drains its scheduled completions before the CPU allowance changes.
// The same resident conversations remain in memory for both groups.
func measureReleaseGroup(b *testing.B, name string, gates []*rateGate, from, seconds int,
	rates []int, finished <-chan rateCompletion, recorder *rateRecorder,
	subscriber *rateSubscriber,
) (int, map[string]any) {
	b.Helper()
	initialCPU, start := cpuSeconds(), time.Now()
	initialBytes, initialSnapshots := recorder.bytes.Load(), recorder.snapshotBytes.Load()
	initialEvents := subscriber.events.Load()
	released := from
	for _, rate := range rates {
		phase := time.Now()
		for index := 0; index < rate*seconds && released < len(gates); index++ {
			planned := phase.Add(time.Duration(float64(index) / float64(rate) * float64(time.Second)))
			time.Sleep(time.Until(planned))
			gate := gates[released]
			gate.planned = planned
			gate.rate = rate
			close(gate.ready)
			released++
		}
		time.Sleep(time.Until(phase.Add(time.Duration(seconds) * time.Second)))
	}
	durations := map[int][]float64{}
	for range released - from {
		select {
		case result := <-finished:
			if result.err != nil {
				b.Fatal(result.err)
			}
			durations[result.rate] = append(durations[result.rate], result.seconds)
		case <-time.After(3 * time.Minute):
			b.Fatal("completion did not finish")
		}
	}
	elapsed, cpu := time.Since(start).Seconds(), cpuSeconds()-initialCPU
	phases := []map[string]any{}
	for _, rate := range rates {
		values := durations[rate]
		sort.Float64s(values)
		if len(values) == 0 {
			continue
		}
		phases = append(phases, map[string]any{"arrival_turns_per_second": rate, "completed": len(values),
			"p50_ms": values[(len(values)-1)*50/100] * 1000,
			"p95_ms": values[(len(values)-1)*95/100] * 1000,
			"p99_ms": values[(len(values)-1)*99/100] * 1000, "max_ms": values[len(values)-1] * 1000})
	}
	result := map[string]any{"name": name, "completed": released - from, "elapsed_seconds": elapsed,
		"cpu_seconds": cpu, "cpu_ms_per_completion": cpu * 1000 / float64(released-from),
		"phases":               phases,
		"saved_batch_bytes":    recorder.bytes.Load() - initialBytes,
		"saved_snapshot_bytes": recorder.snapshotBytes.Load() - initialSnapshots,
		"subscriber_events":    subscriber.events.Load() - initialEvents}
	data, err := json.Marshal(result)
	if err != nil {
		b.Fatal(err)
	}
	fmt.Println("LOAD_GROUP", string(data))
	return released, result
}

func waitRateQuota(b *testing.B, suffix string, data []byte) {
	b.Helper()
	if path := os.Getenv("GENT_BENCH_GATE"); path != "" {
		path += suffix
		if err := os.WriteFile(path+".ready", data, 0o600); err != nil {
			b.Fatal(err)
		}
		for {
			_, err := os.Stat(path)
			if err == nil {
				break
			}
			if !errors.Is(err, os.ErrNotExist) {
				b.Fatal(err)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
}
