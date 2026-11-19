package models

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/compaction"
	"github.com/rickchristie/gent/format"
	"github.com/stretchr/testify/assert"
)

// Capture an accepted storage boundary, then interrupt the worker. The saved bytes are fixed
// before interruption handling runs, as they would be if that process stopped at this boundary.
type providerResumeStore struct {
	view   gent.RunView
	codecs *gent.CodecRegistry
	stop   func(*gent.SaveBatch) bool
	cancel context.CancelFunc
	saved  []byte
}

func (s *providerResumeStore) Save(
	_ context.Context, batch *gent.SaveBatch,
	captureRunStateSnapshot func() (*gent.RunState, error),
) error {
	if s.view.RunId == "" {
		s.view = *gent.NewRunView(batch.RunId)
	}
	encoded, err := s.codecs.Marshal(batch)
	if err != nil {
		return err
	}
	var owned gent.SaveBatch
	if err := s.codecs.Unmarshal(encoded, &owned); err != nil {
		return err
	}
	// The store owns this one archive. Decode only the borrowed batch for ownership transfer;
	// copying the entire growing archive at every save adds no recovery coverage.
	applied, err := s.view.Apply(&owned, s.codecs)
	if err != nil {
		return err
	}
	if applied.Status != gent.ApplyApplied {
		return fmt.Errorf("provider resume archive: %s: %s", applied.Status, applied.Detail)
	}
	if s.saved != nil || s.stop == nil || !s.stop(batch) {
		return nil
	}
	state, err := captureRunStateSnapshot()
	if err != nil {
		return err
	}
	s.saved, err = s.codecs.MarshalRunState(state)
	if err != nil {
		return err
	}
	s.cancel()
	return nil
}

func providerResumeBoundary(name string) func(*gent.SaveBatch) bool {
	responses := 0
	return func(batch *gent.SaveBatch) bool {
		if name == "model response" {
			for _, event := range batch.Events {
				if event.Type == "model_call_finished" {
					responses++
				}
			}
			// Model evidence is grouped with the rejected iteration, with no extra model save.
			return responses == 2
		}
		checkpoint := batch.Checkpoint
		if checkpoint == nil {
			return false
		}
		switch name {
		case "tool result":
			return checkpoint.Phase == gent.PhaseToolCompleted
		case "rejected output":
			return checkpoint.Phase == gent.PhaseIterationCompleted &&
				checkpoint.Contexts[0].Iteration == 2
		case "compacted history":
			return checkpoint.Phase == gent.PhaseCompactionCompleted
		default:
			panic("unknown resume boundary")
		}
	}
}

func configureProviderResume(config *gent.Config, model gent.Model, compact bool) {
	if compact {
		config.CompactionTrigger = compaction.NewStatThresholdTrigger().OnCounter(gent.SCToolCalls, 1)
		config.CompactionStrategy = compaction.NewSummarization(model, format.NewMarkdown()).
			WithKeepRecent(1)
	}
}

func checkProviderResume(t *testing.T, base context.Context, build func() gent.Model, record string,
	compact bool, stop func(*gent.SaveBatch) bool,
) *gent.ExecutionContext {
	t.Helper()
	worker, cancel := context.WithCancel(base)
	defer cancel()
	codecs := gent.NewCodecRegistry()
	store := &providerResumeStore{codecs: codecs, stop: stop, cancel: cancel}
	var calls []providerLookupInput
	var values []providerLoopValue
	model := build()
	agent, config := providerToolJSONAgent(t, model, record, nil, &calls, &values)
	config.Codecs, config.Recorder, config.Limits = codecs, store, liveLimits()
	configureProviderResume(config, model, compact)
	task := providerToolJSONTask(agent.Output())
	if compact {
		for _, text := range []string{
			"Read the record.", "Keep its exact text.", "Use the lookup tool.",
		} {
			task.Messages = append(task.Messages, gent.TextParts(gent.ChatMessageTypeHuman, text))
		}
	}
	runner, execCtx := newModelExecution(t, worker, agent, config)
	value, result, err := runner.NewTurn(execCtx, task)
	if stop != nil {
		if !assert.ErrorIs(t, err, context.Canceled) || !assert.NotEmpty(t, store.saved) {
			return execCtx
		}
		assert.Nil(t, result)
		assert.Equal(t, []providerLookupInput{{7}}, calls)
		// Replace every runtime binding. The stored archive remains with the application.
		model = build()
		agent, config = providerToolJSONAgent(t, model, record, nil, &calls, &values)
		codecs = gent.NewCodecRegistry()
		store.codecs = codecs
		config.Codecs, config.Recorder, config.Limits = codecs, store, liveLimits()
		configureProviderResume(config, model, compact)
		runner, execCtx = restoreModelExecution(t, base, agent, config, store.saved)
		value, result, err = runner.Continue(execCtx)
	}
	if !assert.NoError(t, err) {
		logCompactionFailure(t, execCtx)
		return execCtx
	}
	assert.Equal(t, providerLoopValue{record, 2}, value)
	assert.Equal(t, &gent.ExecutionResult{Status: gent.ExecutionFinished,
		TurnId: execCtx.TurnId(), TerminationReason: gent.TerminationSuccess,
		Output: []gent.ContentPart{gent.TextPart(
			fmt.Sprintf(`{"record":%q,"round":2}`, record))}}, result)
	assert.Equal(t, []providerLookupInput{{7}}, calls, "completed tool work must not run again")
	assert.Equal(t, []providerLoopValue{{record, 1}, {record, 2}}, values)
	assert.Equal(t, int64(1), execCtx.Stats().GetCounter(gent.SCToolCalls))
	assert.Equal(t, int64(1), execCtx.Stats().GetCounter(gent.SCOutputCustomRejectedTotal))
	// Rebuild usage from the complete accepted archive, including the part before restoration.
	var input, output int64
	for _, event := range store.view.Activity {
		if event.Type != "model_call_finished" {
			continue
		}
		after := event.Payload.(*gent.AfterModelCallEvent)
		if !assert.NoError(t, after.Error) || !assert.NotNil(t, after.Response) {
			return execCtx
		}
		input += int64(after.Response.Info.InputTokens)
		output += int64(after.Response.Info.OutputTokens)
	}
	assert.Positive(t, input)
	assert.Positive(t, output)
	assert.Equal(t, input, execCtx.Stats().GetCounter(gent.SCInputTokens))
	assert.Equal(t, output, execCtx.Stats().GetCounter(gent.SCOutputTokens))
	assert.Equal(t, input+output, execCtx.Stats().GetCounter(gent.SCTotalTokens))
	return execCtx
}

func providerResumeResponse(t *testing.T, provider, text string, tool bool) string {
	t.Helper()
	response := providerResponse(provider, 0)
	if !tool {
		response = providerTextResponse(t, provider, text)
	}
	var body map[string]any
	if !assert.NoError(t, json.Unmarshal([]byte(response), &body)) {
		t.FailNow()
	}
	switch provider {
	case "openai", "xai":
		body["usage"] = map[string]any{"input_tokens": 12, "output_tokens": 8, "total_tokens": 20}
	case "anthropic":
		body["usage"] = map[string]any{"input_tokens": 12, "output_tokens": 8}
	case "gemini", "vertexai":
		body["usageMetadata"] = map[string]any{"promptTokenCount": 12, "candidatesTokenCount": 8,
			"totalTokenCount": 20}
	case "ollama":
		body["prompt_eval_count"], body["eval_count"] = 12, 8
	default:
		body["usage"] = map[string]any{"prompt_tokens": 12, "completion_tokens": 8, "total_tokens": 20}
	}
	data, err := json.Marshal(body)
	if !assert.NoError(t, err) {
		t.FailNow()
	}
	return string(data)
}

func TestProvidersUnfinishedResume(t *testing.T) {
	type input struct {
		provider, boundary string
		compact            bool
	}
	type expected struct {
		requests int
		tokens   int64
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	var cases []testCase
	for _, provider := range testProviders {
		for _, boundary := range []string{
			"model response", "tool result", "rejected output", "compacted history",
		} {
			compact := boundary == "compacted history"
			want := expected{3, 60}
			if compact {
				want = expected{4, 80}
			}
			cases = append(cases, testCase{provider + "/" + boundary,
				input{provider, boundary, compact}, want})
		}
	}
	// The uninterrupted request sequence is the same for the three non-compaction boundaries.
	// Save it once per provider and mode; each interruption still uses a separate run and restore.
	baselines := make(map[string][]map[string]any)
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			key := fmt.Sprintf("%s/%t", test.input.provider, test.input.compact)
			for _, resumed := range []bool{false, true} {
				if !resumed && baselines[key] != nil {
					continue
				}
				var requests []map[string]any
				turn := 0
				build := func() gent.Model {
					client := &http.Client{Transport: responseTransport(func(request *http.Request) (
						*http.Response, error,
					) {
						if request.Method == http.MethodGet {
							return fixtureHTTP(routerFixture, "application/json"), nil
						}
						var body map[string]any
						if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
							return nil, err
						}
						requests = append(requests, body)
						if test.input.compact && len(requests) == 2 {
							return fixtureHTTP(providerResumeResponse(t, test.input.provider,
								"Earlier notes say to use the exact lookup result.", false),
								"application/json"), nil
						}
						turn++
						return fixtureHTTP(providerResumeResponse(t, test.input.provider,
							fmt.Sprintf(`{"record":"record-seven","round":%d}`, turn-1), turn == 1),
							"application/json"), nil
					})}
					return fixtureModel(test.input.provider, client)
				}
				var stop func(*gent.SaveBatch) bool
				if resumed {
					stop = providerResumeBoundary(test.input.boundary)
				}
				execCtx := checkProviderResume(t, context.Background(), build, "record-seven",
					test.input.compact, stop)
				assert.Equal(t, test.expected, expected{len(requests),
					execCtx.Stats().GetCounter(gent.SCTotalTokens)})
				if !resumed {
					baselines[key] = requests
					continue
				}
				// Compare every complete wire request, including saved signatures, tool IDs,
				// correction steering, synthetic summaries, output controls, and tool results.
				assert.Equal(t, baselines[key], requests)
				assertProviderResumeNativeHistory(t, test.input.provider, requests[len(requests)-1],
					test.input.compact)
			}
		})
	}
}

func TestLiveProviderUnfinishedResume(t *testing.T) {
	for _, provider := range liveProviders() {
		t.Run(provider.name, func(t *testing.T) {
			config := liveConfig(t, provider, false)
			t.Parallel()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			build := func() gent.Model { return newLiveModel(t, provider, &config) }
			checkProviderResume(t, ctx, build, "record-"+rand.Text(), false,
				providerResumeBoundary("tool result"))
		})
	}
}
