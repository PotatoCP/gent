package examples

import (
	"context"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/agents/react"
	"github.com/rickchristie/gent/compaction"
	"github.com/rickchristie/gent/executor"
	"github.com/rickchristie/gent/toolchain"
	"github.com/stretchr/testify/assert"
)

// The displayed setup runs here so a component example also proves its connection to execution.
func TestRunnerSetupExamples(t *testing.T) {
	type input struct {
		create func(gent.Model) *executor.Runner[string]
	}
	type expected struct {
		answer   string
		requests int
		messages []*gent.ModelMessage
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	messages := []*gent.ModelMessage{
		gent.TextParts(gent.ChatMessageTypeHuman, "Run the example."),
	}
	cases := []testCase{
		{"model", input{func(model gent.Model) *executor.Runner[string] {
			// docs:start model-runner
			loop := react.NewAgent(model)
			runner := executor.NewRunner(loop, &gent.Config{Limits: gent.DefaultLimits()})
			// docs:end model-runner
			return runner
		}}, expected{"Ready.", 1, messages}},
		{"ReAct settings", input{func(model gent.Model) *executor.Runner[string] {
			loop := reactOptions(model)
			// docs:start react-runner
			runner := executor.NewRunner(loop, &gent.Config{Limits: gent.DefaultLimits()})
			// docs:end react-runner
			return runner
		}}, expected{"Ready.", 1, messages}},
		{"shared configuration", input{func(model gent.Model) *executor.Runner[string] {
			standard := configuration()[1]
			// docs:start config-runner
			loop := react.NewAgent(model)
			runner := executor.NewRunner(loop, standard)
			// docs:end config-runner
			return runner
		}}, expected{"Ready.", 1, messages}},
		{"Hooks", input{func(model gent.Model) *executor.Runner[string] {
			hooks := requestHooks()
			// docs:start hooks-runner
			loop := react.NewAgent(model)
			runner := executor.NewRunner(loop, &gent.Config{Hooks: hooks})
			// docs:end hooks-runner
			return runner
		}}, expected{"Ready.", 1, append(append([]*gent.ModelMessage(nil), messages...),
			gent.TextParts(gent.ChatMessageTypeHuman, "Use metric units."))}},
		{"compaction", input{func(model gent.Model) *executor.Runner[string] {
			config := compactConfig(&FixedModel{Text: "Summary."})
			// docs:start compaction-runner
			loop := react.NewAgent(model)
			runner := executor.NewRunner(loop, config)
			// docs:end compaction-runner
			return runner
		}}, expected{"Ready.", 1, messages}},
		{"custom output", input{func(model gent.Model) *executor.Runner[string] {
			// docs:start output-custom-runner
			out := &TrimmedOutput{}
			loop := react.NewWithOutput(model, out)
			runner := executor.NewRunner(loop, &gent.Config{})
			// docs:end output-custom-runner
			return runner
		}}, expected{"Ready.", 1, messages}},
		{"custom loop", input{func(model gent.Model) *executor.Runner[string] {
			// docs:start loop-runner
			loop := &LocalLoop{}
			runner := executor.NewRunner(loop, &gent.Config{})
			// This loop needs no model. NewTurn still uses normal saves and result handling.
			// docs:end loop-runner
			return runner
		}}, expected{"Ready.", 0, nil}},
		{"custom model", input{func(_ gent.Model) *executor.Runner[string] {
			// docs:start custom-model-runner
			model := &FixedModel{Text: "Ready."}
			loop := react.NewAgent(model)
			runner := executor.NewRunner(loop, &gent.Config{})
			// docs:end custom-model-runner
			return runner
		}}, expected{"Ready.", 0, nil}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			model := &scriptModel{Answer: "Ready."}
			answer, _ := runText(t, test.input.create(model))
			assert.Equal(t, test.expected.answer, answer)
			if !assert.Len(t, model.Requests, test.expected.requests) {
				return
			}
			if test.expected.requests > 0 {
				assert.Equal(t, test.expected.messages, model.Requests[0].Messages)
			}
		})
	}
}

func TestRecorderSetupExamples(t *testing.T) {
	type input struct {
		create func(gent.Model, *gent.CodecRegistry, ByteStore) *executor.Runner[string]
	}
	type expected struct {
		answer  string
		capture bool
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"batches", input{func(
			model gent.Model, codecs *gent.CodecRegistry, store ByteStore,
		) *executor.Runner[string] {
			// docs:start recorder-runner
			recorder := &BatchRecorder{Codecs: codecs, Store: store}
			loop := react.NewAgent(model)
			config := &gent.Config{
				Codecs:   codecs,
				Recorder: recorder,
			}
			runner := executor.NewRunner(loop, config)
			// Gent calls Save at its savepoints. The application does not call Save itself.
			// docs:end recorder-runner
			return runner
		}}, expected{"Saved.", false}},
		{"RunStateSnapshots", input{func(model gent.Model, codecs *gent.CodecRegistry,
			store ByteStore,
		) *executor.Runner[string] {
			interval := uint64(1)
			// docs:start snapshot-runner
			recorder := &SnapshotRecorder{
				BatchRecorder: BatchRecorder{Codecs: codecs, Store: store},
				Every:         interval,
			}
			loop := react.NewAgent(model)
			runner := executor.NewRunner(loop, &gent.Config{Codecs: codecs, Recorder: recorder})
			// docs:end snapshot-runner
			return runner
		}}, expected{"Saved.", true}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			codecs, store := gent.NewCodecRegistry(), NewMemoryStore()
			runner := test.input.create(&FixedModel{Text: "Saved."}, codecs, store)
			execCtx, err := runner.NewExecutionContext(context.Background(), "storage")
			if !assert.NoError(t, err) {
				return
			}
			defer execCtx.Release()
			answer, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Save this.")))
			assert.NoError(t, err)
			assert.Equal(t, test.expected.answer, answer)
			assert.NoError(t, checkOutputResult(result))
			saved := store.Read(execCtx.RunId())
			assert.NotEmpty(t, saved.Batches)
			assert.Equal(t, test.expected.capture, len(saved.State) > 0)
			state, err := replayBatches(execCtx.RunId(), saved.Batches, codecs)
			if !assert.NoError(t, err) {
				return
			}
			if test.expected.capture {
				loaded, err := codecs.UnmarshalRunState(saved.State)
				assert.NoError(t, err)
				assert.Equal(t, gent.RunStateReady, loaded.Status)
				fromBatches, err := codecs.MarshalRunState(state)
				assert.NoError(t, err)
				fromCapture, err := codecs.MarshalRunState(loaded.State)
				assert.NoError(t, err)
				assert.Equal(t, fromBatches, fromCapture)
			}
			restored, err := restoreContext(context.Background(), runner, state)
			if !assert.NoError(t, err) {
				return
			}
			defer restored.Release()
			last, lastResult, err := runner.GetLastTurnResult(restored)
			assert.NoError(t, err)
			assert.Equal(t, answer, last)
			assert.Equal(t, result, lastResult)
		})
	}
}

func TestToolChainSetupExamples(t *testing.T) {
	type input struct {
		create func(gent.Model, gent.Tool[*lookupInput]) (*executor.Runner[string], error)
		call   *gent.ModelToolCallRequest
	}
	type expected struct {
		answer    string
		toolCalls int64
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	lookup := toolCall("lookup_order", `{"id":"ABC"}`)
	cases := []testCase{
		{"schema tool", input{func(model gent.Model, tool gent.Tool[*lookupInput]) (
			*executor.Runner[string], error,
		) {
			// docs:start schema-runner
			chain := toolchain.New()
			chain.RegisterTool(tool)
			loop := react.NewAgent(model)
			runner := executor.NewRunner(loop, &gent.Config{ToolChain: chain})
			// docs:end schema-runner
			return runner, nil
		}, lookup}, expected{"Ready.", 1}},
		{"recovery tool", input{func(model gent.Model, _ gent.Tool[*lookupInput]) (
			*executor.Runner[string], error,
		) {
			tool := recoveringTool(&orderService{}, &fixtureLedger{})
			// docs:start recovery-runner
			chain := toolchain.New()
			chain.RegisterTool(tool)
			loop := react.NewAgent(model)
			runner := executor.NewRunner(loop, &gent.Config{ToolChain: chain})
			// Restore with this binding so Gent can inspect unfinished saved calls.
			// docs:end recovery-runner
			return runner, nil
		}, lookup}, expected{"Ready.", 1}},
		{"discovery", input{func(model gent.Model, tool gent.Tool[*lookupInput]) (
			*executor.Runner[string], error,
		) {
			chain, err := discovery(tool)
			if err != nil {
				return nil, err
			}
			// docs:start discovery-runner
			loop := react.NewAgent(model)
			runner := executor.NewRunner(loop, &gent.Config{ToolChain: chain})
			// docs:end discovery-runner
			return runner, nil
		}, toolCall("execute_tool", `{"tool":"lookup_order","args":{"id":"ABC"}}`)},
			expected{"Ready.", 1}},
		{"programmatic calls", input{func(model gent.Model, tool gent.Tool[*lookupInput]) (
			*executor.Runner[string], error,
		) {
			wrapped := programmatic(toolchain.New().RegisterTool(tool))
			// docs:start programmatic-runner
			loop := react.NewAgent(model)
			runner := executor.NewRunner(loop, &gent.Config{ToolChain: wrapped})
			// docs:end programmatic-runner
			return runner, nil
		}, toolCall("run_code", `{"code":"tool.call({tool:'lookup_order',args:{id:'ABC'}});"}`)},
			expected{"Ready.", 2}},
		{"custom chain", input{func(model gent.Model, tool gent.Tool[*lookupInput]) (
			*executor.Runner[string], error,
		) {
			// docs:start custom-chain-runner
			chain := &SelectedTools{
				Registry: toolchain.New(),
				Allowed:  map[string]bool{"lookup_order": true},
			}
			chain.RegisterTool(tool)
			loop := react.NewAgent(model)
			runner := executor.NewRunner(loop, &gent.Config{ToolChain: chain})
			// docs:end custom-chain-runner
			return runner, nil
		}, lookup}, expected{"Ready.", 1}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			tool, err := schemaExamples()
			if !assert.NoError(t, err) {
				return
			}
			model := &scriptModel{Calls: []*gent.ModelToolCallRequest{test.input.call},
				Answer: "Ready."}
			runner, err := test.input.create(model, tool)
			if !assert.NoError(t, err) {
				return
			}
			execCtx, err := runner.NewExecutionContext(context.Background(), "tools")
			if !assert.NoError(t, err) {
				return
			}
			defer execCtx.Release()
			answer, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Look up ABC.")))
			assert.NoError(t, err)
			assert.Equal(t, test.expected.answer, answer)
			assert.NoError(t, checkOutputResult(result))
			assert.Equal(t, test.expected.toolCalls, execCtx.Stats().GetCounter(gent.SCToolCalls))
			assert.Equal(t, int64(0), execCtx.Stats().GetCounter(gent.SCToolCallsErrorTotal))
		})
	}
}

func TestTaskSubmissionExamples(t *testing.T) {
	type input struct {
		task *gent.Task
	}
	type expected struct {
		answer   string
		messages []*gent.ModelMessage
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	tasks := taskExamples()
	cache := cachedTask(cachedPart())
	cases := []testCase{
		{"text", input{tasks[0]}, expected{"Ready.", tasks[0].Messages}},
		{"system prefix", input{tasks[1]}, expected{"Ready.",
			append(append([]*gent.ModelMessage(nil), tasks[1].Instructions...), tasks[1].Messages...)}},
		{"empty prefix", input{tasks[3]}, expected{"Ready.", tasks[3].Messages}},
		{"cache hint", input{cache}, expected{"Ready.",
			append(append([]*gent.ModelMessage(nil), cache.Instructions...), cache.Messages...)}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			model := &scriptModel{Answer: "Ready."}
			runner := executor.NewRunner(react.NewAgent(model), &gent.Config{})
			execCtx, err := runner.NewExecutionContext(context.Background(), "task")
			if !assert.NoError(t, err) {
				return
			}
			defer execCtx.Release()
			answer, result, err := submitTask(runner, execCtx, test.input.task)
			assert.NoError(t, err)
			assert.Equal(t, test.expected.answer, answer)
			assert.NoError(t, checkOutputResult(result))
			if assert.Len(t, model.Requests, 1) {
				assert.Equal(t, test.expected.messages, model.Requests[0].Messages)
			}
		})
	}
}

func TestContentSubmissionExample(t *testing.T) {
	model := &scriptModel{Answer: "Ready."}
	runner := executor.NewRunner(react.NewAgent(model), &gent.Config{})
	execCtx, err := runner.NewExecutionContext(context.Background(), "parts")
	if !assert.NoError(t, err) {
		return
	}
	defer execCtx.Release()
	parts, err := gent.NewParts().Text("Read this data.").Data(map[string]any{"count": 2}).Build()
	if !assert.NoError(t, err) {
		return
	}
	answer, result, err := submitParts(runner, execCtx, parts)
	assert.NoError(t, err)
	assert.Equal(t, "Ready.", answer)
	assert.NoError(t, checkOutputResult(result))
	if assert.Len(t, model.Requests, 1) {
		assert.Equal(t, []*gent.ModelMessage{{Role: gent.ChatMessageTypeHuman, Parts: parts}},
			model.Requests[0].Messages)
	}
}

func TestCodecRunnerExample(t *testing.T) {
	codecs, err := codecExample()
	if !assert.NoError(t, err) {
		return
	}
	model := &FixedModel{Text: `{"summary":"Ready","count":2}`}
	out, err := schemaOutput()
	if !assert.NoError(t, err) {
		return
	}
	loop := react.NewWithOutput(model, out)
	// docs:start codec-runner
	config := &gent.Config{Codecs: codecs}
	runner := executor.NewRunner(loop, config)
	// The same registry must encode the recorder's batches and decode saved state.
	// docs:end codec-runner
	execCtx, err := runner.NewExecutionContext(context.Background(), "codecs")
	if !assert.NoError(t, err) {
		return
	}
	defer execCtx.Release()
	answer, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Summarize.")))
	assert.NoError(t, err)
	assert.Equal(t, Answer{Summary: "Ready", Count: 2}, answer)
	assert.NoError(t, checkOutputResult(result))
	last, lastResult, err := runner.GetLastTurnResult(execCtx)
	assert.NoError(t, err)
	assert.Equal(t, answer, last)
	assert.Equal(t, result, lastResult)
}

func TestStreamingRunnerExample(t *testing.T) {
	ui := &EventLog{}
	model := &FixedModel{Text: "Ready."}
	// docs:start stream-runner
	loop := react.NewAgent(model)
	runner := executor.NewRunner(loop, &gent.Config{
		Subscribers: []*gent.SubscriberRegistration{
			{Subscriber: ui, Delivery: gent.SubscriberLive},
		},
	})
	// ui implements gent.Subscriber. Its OnEvent receives each delivery envelope.
	// docs:end stream-runner
	answer, _ := runText(t, runner)
	assert.Equal(t, "Ready.", answer)
	var accepted []*gent.RecordedEvent
	var sources []string
	for _, event := range ui.Events {
		key, live := eventKeys(event)
		assert.Equal(t, AcceptedKey{event.RunId, event.EventSequenceNumber}, key)
		assert.Equal(t, LiveKey{key, event.AttemptId}, live)
		if call, ok := event.Payload.(*gent.AfterModelCallEvent); ok {
			sources = append(sources, call.Response.Choices[0].CandidateSource.MessageIds...)
		}
		if event.Type == "accepted_answer" {
			chunk := event.Payload.(*gent.StreamChunk)
			assert.Equal(t, chunk.Content, contentPreview(event))
			accepted = append(accepted, event)
		}
	}
	if assert.Len(t, accepted, 1) {
		chunk := accepted[0].Payload.(*gent.StreamChunk)
		assert.Len(t, sources, 1)
		assert.Equal(t, sources, chunk.SourceMessageIds)
		assert.Equal(t, []gent.ContentPart{&gent.AssistantContent{
			Type: gent.AssistantContentFinalAnswer, MessageId: accepted[0].TurnId + ":answer",
			Content: []gent.ContentPart{gent.TextPart("Ready.")},
		}}, chunk.Content)
	}
	assert.Len(t, ui.Stops, 1)
	assert.NoError(t, ui.Stops[0].Error)
}

func TestTimedExecutionExample(t *testing.T) {
	runner := executor.NewRunner(&LocalLoop{}, &gent.Config{})
	answer, result, err := timedExecution(context.Background(), runner,
		gent.NewTask(gent.TextPart("Start.")))
	assert.NoError(t, err)
	assert.Equal(t, "Ready.", answer)
	assert.NoError(t, checkOutputResult(result))
}

func TestCustomCompactionSetupExamples(t *testing.T) {
	type input struct {
		configure func(*gent.Config)
	}
	type expected struct {
		compactions int64
		summaries   int
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"custom trigger and selector", input{func(config *gent.Config) {
			// docs:start compaction-trigger-use
			config.CompactionTrigger = &LengthTrigger{Maximum: 4}
			// docs:end compaction-trigger-use
			// docs:start compaction-strategy-use
			config.CompactionStrategy = &RecentContext{Keep: 2}
			// docs:end compaction-strategy-use
		}}, expected{1, 0}},
		{"custom retention", input{func(config *gent.Config) {
			config.CompactionTrigger = &LengthTrigger{Maximum: 4}
			strategy := config.CompactionStrategy.(*compaction.SummarizationStrategy)
			strategy.WithKeepRecent(0)
			// docs:start compaction-retention-use
			strategy.WithUserMessageRetention(&gent.UserMessageRetentionConfig{
				MaxBytes: 32 * 1024,
				Strategy: &NoUserRetention{},
			})
			// docs:end compaction-retention-use
		}}, expected{1, 1}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			summary := &scriptModel{Answer: "Summary."}
			config := compactConfig(summary)
			test.input.configure(config)
			runner := executor.NewRunner(react.NewAgent(&FixedModel{Text: "Ready."}), config)
			execCtx, err := runner.NewExecutionContext(context.Background(), "compaction")
			if !assert.NoError(t, err) {
				return
			}
			defer execCtx.Release()
			for range 3 {
				answer, result, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Continue.")))
				if !assert.NoError(t, err) {
					return
				}
				assert.Equal(t, "Ready.", answer)
				assert.NoError(t, checkOutputResult(result))
			}
			assert.Equal(t, test.expected, expected{
				execCtx.Stats().GetCounter(gent.SCCompactions), len(summary.Requests),
			})
		})
	}
}
