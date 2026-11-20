package examples

import (
	"context"
	"testing"
	"time"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/agents/react"
	"github.com/rickchristie/gent/executor"
	"github.com/rickchristie/gent/format"
	"github.com/rickchristie/gent/toolchain"
	"github.com/rickchristie/gent/trace"
	"github.com/stretchr/testify/assert"
)

func TestHooksAndStatistics(t *testing.T) {
	model := &scriptModel{Answer: "Ready."}
	config := &gent.Config{Hooks: requestHooks()}
	runText(t, executor.NewRunner(react.NewAgent(model), config))
	assert.Equal(t, []*gent.ModelMessage{
		gent.TextParts(gent.ChatMessageTypeHuman, "Run the example."),
		gent.TextParts(gent.ChatMessageTypeHuman, "Use metric units."),
	}, model.Requests[0].Messages)
	service := &orderService{}
	tool, err := boundTool(service)
	assert.NoError(t, err)
	chain := toolchain.New().RegisterTool(tool)
	config.ToolChain = chain
	runCheck(t, config, func(execCtx *gent.ExecutionContext) error {
		total, turn := counters(execCtx)
		assert.Equal(t, int64(1), total)
		assert.Equal(t, int64(1), turn)
		assert.Equal(t, int64(1), execCtx.Stats().GetCounter("app:checked_files"))
		plan, err := chain.PrepareCalls(execCtx, []*gent.ModelToolCallRequest{
			toolCall("lookup_order", `{"id":" ABC "}`)})
		if err != nil {
			return err
		}
		record, err := chain.ExecuteNext(execCtx, plan, format.NewXML())
		if err != nil {
			return err
		}
		assert.Equal(t, &gent.ToolExecutionResponse{Status: gent.ToolExecuted,
			Parts: []gent.ContentPart{gent.TextPart("ABC")}}, record.Execution())
		return nil
	})
	for _, config := range configuration() {
		runText(t, executor.NewRunner(react.NewAgent(&FixedModel{Text: "Ready."}), config))
	}
}

func TestObservationExamples(t *testing.T) {
	// docs:start subscriber-handlers
	live, accepted := &EventLog{}, &EventLog{}
	// docs:end subscriber-handlers
	config := subscriberConfig(live, accepted)
	model := &FixedModel{Text: "Ready."}
	// docs:start subscriber-runner
	loop := react.NewAgent(model)
	runner := executor.NewRunner(loop, config)
	// The configured handlers receive events from NewTurn and Continue.
	// docs:end subscriber-runner
	runText(t, runner)
	assert.Equal(t, accepted.Events, live.Events)
	if !assert.Len(t, accepted.Stops, 1) || !assert.Len(t, live.Stops, 1) {
		return
	}
	assert.Equal(t, gent.SubscriberClosed, accepted.Stops[0].Reason)
	assert.Equal(t, "Delivered through 140; accepted through 150", deliveredPosition(
		&gent.SubscriberStop{LastDeliveredEventSequenceNumber: 140,
			LastAcceptedEventSequenceNumber: 150}))
	event := &gent.RecordedEvent{RunId: "run", AttemptId: "attempt", EventSequenceNumber: 7,
		Payload: &gent.StreamChunk{Content: []gent.ContentPart{gent.TextPart("Part")}}}
	key, preview := eventKeys(event)
	assert.Equal(t, AcceptedKey{"run", 7}, key)
	assert.Equal(t, LiveKey{AcceptedKey{"run", 7}, "attempt"}, preview)
	assert.Equal(t, []gent.ContentPart{gent.TextPart("Part")}, contentPreview(event))
	assert.Nil(t, contentPreview(&gent.RecordedEvent{Payload: &gent.BeforeModelCallEvent{}}))
	seq, traced, err := traceRunner(&FixedModel{Text: "Ready."})
	assert.NoError(t, err)
	defer seq.Close()
	subscription, err := traceSubscribe(seq)
	assert.NoError(t, err)
	execCtx, err := traced.NewExecutionContext(context.Background(), "trace")
	if !assert.NoError(t, err) {
		return
	}
	defer execCtx.Release()
	answer, result, err := traced.NewTurn(execCtx, gent.NewTask(gent.TextPart("Start.")))
	assert.NoError(t, err)
	assert.NoError(t, checkOutputResult(result))
	assert.Equal(t, "Ready.", answer)
	view := seq.Snapshot()
	assert.Equal(t, execCtx.RunId(), view.RunId)
	assert.Equal(t, trace.RunStatusSucceeded, view.Status)
	assert.Equal(t, gent.SubscriberClosed, seq.LastDeliveryStop().Reason)
	assert.NoError(t, seq.LastDeliveryStop().Error)
	subscription.Close()
	assert.NoError(t, subscription.Err())
}

func TestControlExamples(t *testing.T) {
	type input struct{ status gent.ExecutionStatus }
	type expected struct{ message string }
	type testCase struct {
		input    input
		expected expected
	}
	cases := []testCase{
		{input{gent.ExecutionFinished}, expected{"The turn finished; inspect TerminationReason."}},
		{input{gent.ExecutionPaused}, expected{"The task remains unfinished."}},
		{input{gent.ExecutionBusy}, expected{"Another attempt already owns this context."}},
		{input{gent.ExecutionNoActiveTurn}, expected{"There is no unfinished turn to continue."}},
	}
	for _, test := range cases {
		t.Run(string(test.input.status), func(t *testing.T) {
			assert.Equal(t, test.expected.message, runStatus(&gent.ExecutionResult{
				Status: test.input.status}))
		})
	}
	before := time.Now()
	ctx, cancel := deadlineContext(context.Background())
	deadline, ok := ctx.Deadline()
	assert.True(t, ok)
	assert.GreaterOrEqual(t, deadline.UnixNano(), before.Add(90*time.Second).UnixNano())
	cancel()
	assert.Equal(t, context.Canceled, ctx.Err())
}

func TestCompactionConfiguration(t *testing.T) {
	config := compactConfig(&FixedModel{Text: "Summary."})
	runText(t, executor.NewRunner(react.NewAgent(&FixedModel{Text: "Ready."}), config))
	runText(t, executor.NewRunner(react.NewAgent(&FixedModel{Text: "Ready."}), windowConfig()))
	runCheck(t, &gent.Config{}, func(execCtx *gent.ExecutionContext) error {
		trigger := &LengthTrigger{Maximum: 100}
		compact, err := trigger.ShouldCompact(execCtx)
		assert.NoError(t, err)
		assert.False(t, compact)
		assert.NoError(t, trigger.NotifyCompacted(execCtx))
		retained, err := (&NoUserRetention{}).Retain(&gent.UserMessageRetentionInput{
			MaxBytes: 40, Marker: "Earlier input omitted.",
			Messages: []*gent.UserMessageRetentionEntry{
				{Message: gent.TextParts(gent.ChatMessageTypeHuman, "Long task")}},
		})
		assert.NoError(t, err)
		assert.Equal(t, &gent.UserMessageRetentionResult{EarlierContentOmitted: true,
			RetainedBytes: len("Earlier input omitted.")}, retained)
		return nil
	})
}

func TestDiscoveryAndScripts(t *testing.T) {
	tool, err := boundTool(&orderService{})
	assert.NoError(t, err)
	searchChain, err := discovery(tool)
	assert.NoError(t, err)
	selected := &SelectedTools{Registry: toolchain.New(),
		Allowed: map[string]bool{"lookup_order": true}}
	selected.RegisterTool(tool)
	wrapper := programmatic(selected)
	runCheck(t, &gent.Config{ToolChain: wrapper}, func(execCtx *gent.ExecutionContext) error {
		definitions, err := selected.NativeTools(execCtx)
		assert.NoError(t, err)
		assert.Len(t, definitions, 1)
		assert.Equal(t, "lookup_order", definitions[0].Function.Name)
		_, err = searchChain.NativeTools(execCtx)
		assert.NoError(t, err)
		plan, err := wrapper.PrepareCalls(execCtx, []*gent.ModelToolCallRequest{
			toolCall("run_code", `{"code":"tool.call({tool:'lookup_order',args:{id:'ABC'}});"}`)})
		if err != nil {
			return err
		}
		record, err := wrapper.ExecuteNext(execCtx, plan, format.NewXML())
		if err != nil {
			return err
		}
		assert.Equal(t, gent.ToolExecuted, record.Execution().Status)
		assert.Equal(t, int64(2), execCtx.Stats().GetCounter(gent.SCToolCalls))
		return nil
	})
}
