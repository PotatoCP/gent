//go:build gent_review

package gent_test

import genttest "github.com/rickchristie/gent/internal/tt"

import (
	"context"
	"errors"
	"fmt"
	"github.com/rickchristie/gent/output"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/agents/react"
	"github.com/rickchristie/gent/executor"
	"github.com/rickchristie/gent/format"
	"github.com/rickchristie/gent/internal/buffer"
	"github.com/rickchristie/gent/search"
	"github.com/rickchristie/gent/toolchain"
	"github.com/rickchristie/gent/toolchain/jsruntime"
	"github.com/rickchristie/gent/trace"
	"github.com/stretchr/testify/assert"
)

// These probes preserve failures from the review. See the parent README before running them.
type reviewLoop struct{ err error }

type reviewModel struct {
	text string
	err  error
}

func (m reviewModel) GenerateContentStream(
	context.Context, *gent.ModelCallRequest,
) (gent.Stream, error) {
	return gent.NewCompletedStream(
		&gent.ModelCallResponse{

			Choices: []*gent.ContentChoice{

				{

					Completion: gent.ModelCompletion{
						Status: gent.ModelStatusAnswer,
					},
					CandidateSource: &gent.ContentSource{
						MessageIds: []string{
							"answer",
						},
					},
					Parts: []gent.ContentPart{
						&gent.AssistantContent{
							Type:      gent.AssistantContentFinalAnswerCandidate,
							MessageId: "answer",
							Content: []gent.ContentPart{
								gent.TextContent{
									Text: m.text,
								},
							},
						},
					},
				},
			},
		},
		m.err,
	), nil
}

func TestReviewSharedAgent(t *testing.T) {
	type input struct {
		format   gent.TextFormat
		response string
	}
	type expected struct {
		result gent.ExecutionResult
		err    error
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	want := expected{
		result: gent.ExecutionResult{
			Status:            gent.ExecutionFinished,
			TerminationReason: gent.TerminationSuccess,
			Output:            []gent.ContentPart{gent.TextContent{Text: "done"}},
		},
	}
	tests := []testCase{
		{"XML", input{format.NewXML(), `done`}, want},
		{"Markdown", input{format.NewMarkdown(), `done`}, want},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			agent := react.NewAgent(reviewModel{
				text: tc.input.response,
			}).WithToolResultFormat(tc.input.format)
			runner := executor.NewRunner(agent, &gent.Config{})
			var contexts []*gent.ExecutionContext
			for i := 0; i < 8; i++ {
				execCtx, err := runner.NewExecutionContext(context.Background(), "review")
				if !assert.NoError(t, err) {
					return
				}
				t.Cleanup(execCtx.Release)
				contexts = append(contexts, execCtx)
			}
			var wg sync.WaitGroup
			start := make(chan struct{})
			for _, execCtx := range contexts {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					_, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("test")))
					assert.Equal(t, tc.expected.err, err)
					want := tc.expected.result
					want.TurnId = execCtx.TurnId()
					assert.Equal(t, &want, result)
				}()
			}
			close(start)
			wg.Wait()
		})
	}
}

type reviewCompletionSubscriber struct {
	entered, release chan struct{}
}

func (subscriber *reviewCompletionSubscriber) OnEvent(
	ctx context.Context, event *gent.RecordedEvent,
) error {
	if event.Type != "model_call_finished" {
		return nil
	}
	close(subscriber.entered)
	<-ctx.Done()
	// Model teardown must join this bounded callback cleanup before the attempt returns.
	<-subscriber.release
	return ctx.Err()
}
func (*reviewCompletionSubscriber) OnStop(context.Context, *gent.SubscriberStop) {}

func TestReviewModelErrorWaitsForCompletion(t *testing.T) {
	subscriber := &reviewCompletionSubscriber{make(chan struct{}), make(chan struct{})}
	unblock := sync.OnceFunc(func() { close(subscriber.release) })
	defer unblock()
	failure := errors.New("provider failed")
	runner, execCtx := newTestExecution(
		t,
		context.Background(),
		react.NewAgent(
			reviewModel{
				err: failure,
			},
		),
		&gent.Config{Subscribers: []*gent.SubscriberRegistration{{Subscriber: subscriber,
			Delivery: gent.SubscriberLive}}},
	)
	done := make(chan error, 1)
	go func() {
		_, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("test")))
		assert.Nil(t, result)
		done <- err
	}()
	select {
	case <-subscriber.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("completion event did not arrive")
	}
	select {
	case <-done:
		t.Error("execution returned before callback cleanup")
	default:
	}
	unblock()
	assert.ErrorIs(t, <-done, failure)
}

func (l reviewLoop) Next(*gent.ExecutionContext) (*gent.AgentLoopResult, error) {
	return &gent.AgentLoopResult{
		TerminationReason: gent.TerminationSuccess,
		Action:            gent.LATerminate,
		Value:             genttest.Value(""),
	}, l.err
}

func TestReviewExecutionReleasesContext(t *testing.T) {
	type input struct{ loopErr error }
	type expected struct {
		err       error
		completed bool
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	failure := errors.New("loop failed")
	for _, test := range []testCase{
		{"success", input{}, expected{completed: true}},
		{"loop error", input{failure}, expected{err: failure}},
	} {
		t.Run(test.name, func(t *testing.T) {
			owner, cancel := context.WithCancel(context.Background())
			defer cancel()
			runner, execCtx := newTestExecution(t, owner, reviewLoop{test.input.loopErr}, nil)
			_, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Run.")))
			assert.ErrorIs(t, err, test.expected.err)
			if test.expected.completed {
				assert.Equal(t, &gent.ExecutionResult{Status: gent.ExecutionFinished, TurnId: execCtx.TurnId(),
					TerminationReason: gent.TerminationSuccess}, result)
			} else {
				assert.Nil(t, result)
			}
			assert.ErrorIs(t, execCtx.Context().Err(), context.Canceled)
			assert.NoError(t, owner.Err())
		})
	}
}

func TestReviewTraceUnsubscribeAfterClose(t *testing.T) {
	before := reviewBufferWorkers()
	seq, err := trace.NewSequencer("", trace.Config{})
	if !assert.NoError(t, err) {
		return
	}
	ch, unsubscribe := seq.Subscribe()
	loop := contentLoop(func(execCtx *gent.ExecutionContext) (*gent.AgentLoopResult, error) {
		for i := 0; i < 16; i++ {
			execCtx.PublishCommonEvent("review:event", "test event", i)
		}
		return acceptRootOutput(execCtx, "Done.")
	})
	runner, execCtx := newTestExecution(t, context.Background(), loop,
		&gent.Config{Subscribers: []*gent.SubscriberRegistration{{Subscriber: seq}}})
	_, _, err = runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Run.")))
	if !assert.NoError(t, err) {
		return
	}
	seq.Close()
	unsubscribe()
	assert.Eventually(t, func() bool { return reviewBufferWorkers() <= before },
		time.Second, time.Millisecond, "unsubscribe must release the drain worker")
	select {
	case event, ok := <-ch:
		assert.False(t, ok, "unsubscribe must discard queued events after Close")
		assert.Nil(t, event)
	case <-time.After(time.Second):
		t.Error("subscription did not close")
	}
	// Drain remaining events so the failed check does not itself leave a blocked worker.
	for range ch {
	}
}

func reviewBufferWorkers() int {
	stack := make([]byte, 1<<20)
	n := runtime.Stack(stack, true)
	count := 0
	for _, entry := range strings.Split(string(stack[:n]), `

`) {
		if strings.Contains(entry, "internal/buffer.(*Delivery") {
			count++
		}
	}
	return count
}

func TestReviewBleveSearchChecksCancellation(t *testing.T) {
	index, err := search.NewBleveIndex(&toolchain.ToolBleveAdapter{})
	if !assert.NoError(t, err) {
		return
	}
	defer func() { assert.NoError(t, index.Close()) }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = index.Search(ctx, "test", 10)
	assert.ErrorIs(t, err, context.Canceled)
}

type reviewIndex struct {
	mu      sync.Mutex
	value   string
	entered chan struct{}
	release chan struct{}
}

func (i *reviewIndex) Add(_ context.Context, _ string, value string) error {
	i.mu.Lock()
	i.value = value
	i.mu.Unlock()
	if value == "old" {
		i.entered <- struct{}{}
		<-i.release
	}
	return nil
}

func (i *reviewIndex) Search(context.Context, string, int) ([]search.SearchResult, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	return []search.SearchResult{{Id: "doc", Snippet: i.value}}, nil
}

func (i *reviewIndex) Remove(string) error                           { return nil }
func (i *reviewIndex) Swap(context.Context, map[string]string) error { return nil }

func TestReviewFusedIndexConcurrentReplacement(t *testing.T) {
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	left := &reviewIndex{entered: entered, release: release}
	right := &reviewIndex{entered: entered, release: release}
	index := search.NewFusedIndex(&search.WeightedLinearFuser{},
		map[string]search.SearchIndex[string]{"left": left, "right": right}, nil)
	done := make(chan error, 1)
	go func() { done <- index.Add(context.Background(), "doc", "old") }()
	<-entered
	newDone := make(chan error, 1)
	go func() { newDone <- index.Add(context.Background(), "doc", "new") }()
	select {
	case err := <-newDone:
		assert.NoError(t, err)
		t.Error("replacement completed before the previous update left its first subindex")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	assert.NoError(t, <-done)
	select {
	case err := <-newDone:
		assert.NoError(t, err)
	case <-time.After(time.Second):
		t.Error("replacement did not complete")
	}
	leftResult, leftErr := left.Search(context.Background(), "test", 1)
	rightResult, rightErr := right.Search(context.Background(), "test", 1)
	assert.NoError(t, leftErr)
	assert.NoError(t, rightErr)
	want := []search.SearchResult{{Id: "doc", Snippet: "new"}}
	assert.Equal(t, want, leftResult)
	assert.Equal(t, want, rightResult)
}

func TestReviewDrainedBufferReleasesValues(t *testing.T) {
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	b := buffer.NewDelivery[[]byte](32, 32<<20, time.Minute)
	for i := 0; i < 32; i++ {
		data := make([]byte, 1<<20)
		data[0] = byte(i)
		assert.True(t, b.Send(data, len(data)))
	}
	b.Close()
	for range b.Receive() {
	}
	<-b.Done()
	assert.NoError(t, b.Err())
	runtime.GC()
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(b)
	retained := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	assert.Less(t, retained, int64(2<<20), "drained values remain reachable")
}

func TestReviewRuntimeRecoversAfterTimeout(t *testing.T) {
	r := jsruntime.New(jsruntime.Config{Timeout: 10 * time.Millisecond, MaxCallStack: 1024})
	_, err := r.Execute(context.Background(), "for (;;) {}")
	if !assert.Error(t, err) {
		return
	}
	result, err := r.Execute(context.Background(), `console.log("ready")`)
	assert.NoError(t, err)
	assert.Equal(t, &jsruntime.Result{ConsoleLog: []string{"ready"}}, result)
}

func TestReviewExpireRecordsDuringAppend(t *testing.T) {
	execCtx := genttest.NewContext(t, &gent.Config{})
	if !assert.NoError(t, execCtx.AppendRecord(&gent.HistoryEntry{}, true)) {
		return
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	stopped := make(chan struct{})
	writesDone := make(chan struct{})
	panicResult := make(chan any, 1)
	wg.Add(2)
	go func() {
		defer wg.Done()
		defer close(writesDone)
		<-start
		for i := 0; i < 300; i++ {
			select {
			case <-stopped:
				return
			default:
			}
			err := execCtx.AppendRecord(&gent.HistoryEntry{}, true)
			if err != nil && !errors.Is(err, gent.ErrHistoryConflict) {
				t.Error(err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		defer close(stopped)
		defer func() { panicResult <- recover() }()
		<-start
		// Keep both operations active. A fixed read count can finish before the first append.
		for {
			select {
			case <-writesDone:
				return
			default:
			}
			if err := execCtx.ExpireRecords(1); err != nil && !errors.Is(err, gent.ErrHistoryConflict) {
				t.Error(err)
				return
			}
		}
	}()
	close(start)
	wg.Wait()
	assert.Nil(t, <-panicResult, "concurrent public history methods must not panic")
}

func BenchmarkReviewEmitChunk(b *testing.B) {
	for _, count := range []int{1000, 2000, 4000, 8000} {
		b.Run(
			fmt.Sprint(count),
			func(b *testing.B) {
				b.ReportAllocs()
				for n := 0; n < b.N; n++ {
					execCtx := genttest.NewContext(b, &gent.Config{})
					for i := 0; i < count; i++ {
						if err := execCtx.EmitChunk(
							gent.StreamChunk{
								Content: []gent.ContentPart{
									&gent.AssistantContent{

										Type:      gent.AssistantContentIntermediary,
										MessageId: "preview",
										Content: []gent.ContentPart{
											gent.TextContent{Text: "x"},
										},
									},
								},
							},
						); err != nil {
							b.Fatal(err)
						}
					}
				}
			},
		)
	}
}

func (reviewLoop) Output() gent.Output[string] { return output.NewTextOutput() }
func (m reviewModel) PrepareRequest(execCtx *gent.ExecutionContext,
	request *gent.ModelCallRequest, chain gent.ToolChain,
) (*gent.ModelCallRequest, error) {
	return gent.PrepareModelRequest(execCtx, request, chain)
}

func newTestExecution(
	t testing.TB,
	ctx context.Context,
	loop gent.Loop[string],
	config *gent.Config,
) (*executor.Runner[string], *gent.ExecutionContext) {
	t.Helper()
	if config == nil {
		config = &gent.Config{}
	}
	runner := executor.NewRunner(loop, config)
	execCtx, err := runner.NewExecutionContext(ctx, "review")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(execCtx.Release)
	return runner, execCtx
}

type contentLoop func(*gent.ExecutionContext) (*gent.AgentLoopResult, error)

func (loop contentLoop) Next(execCtx *gent.ExecutionContext) (*gent.AgentLoopResult, error) {
	return loop(execCtx)
}
func (contentLoop) Output() gent.Output[string] { return output.NewTextOutput() }
func acceptRootOutput(execCtx *gent.ExecutionContext, text string) (*gent.AgentLoopResult, error) {
	return gent.AcceptOutput(execCtx, &gent.OutputResult[string]{Accepted: true,
		Value: text, Content: []gent.ContentPart{gent.TextPart(text)}}, nil)
}
