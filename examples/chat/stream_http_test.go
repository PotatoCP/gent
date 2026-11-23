package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rickchristie/gent"
	"github.com/stretchr/testify/assert"
)

type streamEvidence struct {
	live, accepted map[gent.AssistantContentType]bool
	tools          map[string]bool
}

// Read the real SSE response. Reconnect can supply accepted records in the initial frame;
// later deliveries must retain their Live/Accepted identity and content classification.
func readHTTPStream(ctx context.Context, client *http.Client, url string) (*streamEvidence, error) {
	request, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, fmt.Errorf("stream HTTP status %d", response.StatusCode)
	}
	result := &streamEvidence{live: map[gent.AssistantContentType]bool{},
		accepted: map[gent.AssistantContentType]bool{}, tools: map[string]bool{}}
	add := func(delivery gent.SubscriberDelivery, events ...*displayEvent) {
		for _, event := range events {
			for _, item := range event.Items {
				if item.Kind == itemTool {
					result.tools[item.Label] = true
				}
				if item.Text == "" || item.Type == "" {
					continue
				}
				if delivery == gent.SubscriberLive {
					result.live[item.Type] = true
				} else {
					result.accepted[item.Type] = true
				}
			}
		}
	}
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 64<<10), 64<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var frame struct {
			Kind     frameKind               `json:"kind"`
			Delivery gent.SubscriberDelivery `json:"delivery"`
			Event    *displayEvent           `json:"event"`
			State    struct {
				Active           bool `json:"active"`
				Events, Previews []*displayEvent
			} `json:"state"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &frame); err != nil {
			return nil, err
		}
		switch frame.Kind {
		case frameReset:
			add(gent.SubscriberAccepted, frame.State.Events...)
			add(gent.SubscriberLive, frame.State.Previews...)
			if !frame.State.Active {
				return result, nil
			}
		case frameEvent:
			add(frame.Delivery, frame.Event)
		case frameIdle:
			return result, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("event stream ended before worker completion")
}

func TestHTTPStreamKeepsProgressAndAcceptedAnswerSeparate(t *testing.T) {
	app, _ := testApplication(t, 20*time.Millisecond)
	server := httptest.NewServer(app.routes())
	defer server.Close()
	runId := submitHTTP(t, app, &actionRequest{Action: actionTurn, Prompt: "Find John Smith."})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	evidence, err := readHTTPStream(ctx, server.Client(), server.URL+"/api/events?runId="+runId)
	if !assert.NoError(t, err) {
		return
	}
	assert.Equal(t, map[gent.AssistantContentType]bool{
		gent.AssistantContentCommentary: true, gent.AssistantContentReasoning: true,
		gent.AssistantContentFinalAnswerCandidate: true, gent.AssistantContentFinalAnswer: true,
	}, evidence.accepted)
	assert.True(t, evidence.live[gent.AssistantContentReasoning])
	assert.True(t, evidence.live[gent.AssistantContentFinalAnswerCandidate])
	assert.True(t, evidence.accepted[gent.AssistantContentFinalAnswer])
	assert.Equal(t, "I found John Smith (C001), using john.smith@email.com.",
		savedHTTP(t, app, runId).LastAnswer)
}

// Standard test runs do not load .env. The release script supplies one configured provider at a
// time through envrun, so this test verifies paid calls instead of silently using fixture data.
func TestLiveChatScenarios(t *testing.T) {
	for _, provider := range providerOptions {
		t.Run(provider.name, func(t *testing.T) {
			prefix := "GENT_TEST_" + strings.ToUpper(provider.name) + "_"
			if os.Getenv(prefix+"KEY") == "" {
				t.Skip("provider credentials are not configured")
			}
			environment := map[string]string{}
			for _, suffix := range []string{"KEY", "MODEL", "URL"} {
				environment[prefix+suffix] = os.Getenv(prefix + suffix)
			}
			factory, err := providerModel(provider.name, 0, environment)
			if !assert.NoError(t, err) {
				return
			}
			embedder, err := scenarioEmbedder(provider.name)
			if !assert.NoError(t, err) {
				return
			}
			defer func() { assert.NoError(t, embedder.Close()) }()
			for _, scenario := range scenarios {
				t.Run(string(scenario.Id), func(t *testing.T) {
					ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
					app := newApplication(ctx, factory, provider.name, embedder)
					server := httptest.NewServer(app.routes())
					defer func() { cancel(); app.wait.Wait(); server.Close() }()
					runId := submitHTTP(t, app, &actionRequest{Action: actionTurn,
						Scenario: scenario.Id, Prompt: scenario.Investigation})
					evidence, err := readHTTPStream(ctx, server.Client(),
						server.URL+"/api/events?runId="+runId)
					if !assert.NoError(t, err) {
						return
					}
					state := savedHTTP(t, app, runId)
					if !assert.Equal(t, &attemptOutcome{Status: gent.ExecutionFinished}, state.Outcome) {
						return
					}
					assert.NotEmpty(t, state.LastAnswer)
					assert.Greater(t, len(evidence.tools), 1, "the task must inspect related records")
					assert.True(t, evidence.accepted[gent.AssistantContentFinalAnswer])
					assert.True(t, evidence.accepted[gent.AssistantContentCommentary] ||
						evidence.accepted[gent.AssistantContentIntermediary], "expected tool-work progress")
					if provider.name != "xai" {
						assert.True(t, evidence.accepted[gent.AssistantContentReasoning],
							"the investigation should expose provider reasoning or a summary")
					}
					assert.True(t, evidence.live[gent.AssistantContentFinalAnswerCandidate] ||
						evidence.live[gent.AssistantContentIntermediary])
					t.Logf("%s/%s: live types %v; accepted types %v; tools %v",
						provider.name, scenario.Id, evidence.live, evidence.accepted, evidence.tools)
					// The next HTTP task creates a fresh worker. Native thinking and tool evidence from
					// the investigation must remain usable, and the original user's input must survive.
					next := submitHTTP(t, app, &actionRequest{Action: actionTurn, RunId: runId,
						Prompt: "Do not call tools or change records. What email address did I give you? " +
							"Reply only with that address."})
					continued := savedHTTP(t, app, next)
					assert.Equal(t, runId, next)
					assert.Equal(t, &attemptOutcome{Status: gent.ExecutionFinished}, continued.Outcome)
					email := "john.smith@email.com"
					if scenario.Id == scenarioEcommerce {
						email = "alex.rivera@email.com"
					}
					assert.Equal(t, email, continued.LastAnswer)
					// Both replay paths must restore the same accepted state, including native items.
					data, _ := app.store.read(runId)
					full, err := app.store.load(runId, data, replayBatches)
					if !assert.NoError(t, err) {
						return
					}
					captured, err := app.store.load(runId, data, replayCapture)
					if !assert.NoError(t, err) {
						return
					}
					fullBytes, err := app.store.codecs.MarshalRunState(full)
					assert.NoError(t, err)
					captureBytes, err := app.store.codecs.MarshalRunState(captured)
					assert.NoError(t, err)
					assert.Equal(t, fullBytes, captureBytes)
				})
			}
		})
	}
}
