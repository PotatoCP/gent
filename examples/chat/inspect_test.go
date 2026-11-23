package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/stretchr/testify/assert"
)

func TestInspectionReadsAcceptedStateWithoutExecution(t *testing.T) {
	type input struct {
		kind inspectionKind
		path replayPath
	}
	type expected struct {
		status  int
		calls   int64
		head    uint64
		batches int
	}
	type testCase struct {
		name  string
		input input
	}
	app, calls := testApplication(t, 0)
	first := executeAction(t, app, &actionRequest{Action: actionTurn,
		Prompt: "Find John Smith. Remember Cédar 🌿.", Path: replayCapture})
	last := executeAction(t, app, &actionRequest{Action: actionTurn, RunId: first.RunId,
		Prompt: "What was my first message?", Path: replayCapture})
	before, _ := app.store.read(last.RunId)
	want := expected{200, calls.Load(), before.head, len(before.batches)}
	for _, test := range []testCase{
		{"archive", input{inspectionView, replayBatches}},
		{"state from full replay", input{inspectionState, replayBatches}},
		{"state from capture", input{inspectionState, replayCapture}},
	} {
		t.Run(test.name, func(t *testing.T) {
			query := url.Values{"runId": {last.RunId}, "kind": {string(test.input.kind)},
				"path": {string(test.input.path)}}
			request := httptest.NewRequest(http.MethodGet, "/api/inspect?"+query.Encode(), nil)
			response := httptest.NewRecorder()
			app.routes().ServeHTTP(response, request)
			var result inspection
			if !assert.NoError(t, json.Unmarshal(response.Body.Bytes(), &result)) {
				return
			}
			assert.Equal(t, want, expected{response.Code, calls.Load(),
				result.Head, result.BatchCount})
			assert.Equal(t, test.input.kind, result.Kind)
			assert.Equal(t, last.RunId, result.RunId)
			assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
			var recovered any
			var expectedKeys []string
			if test.input.kind == inspectionView {
				var view gent.RunView
				if assert.NoError(t, app.store.codecs.Unmarshal([]byte(result.Encoded), &view)) {
					original, err := app.store.archive(last.RunId, before)
					assert.NoError(t, err)
					assert.Equal(t, original, &view)
					recovered = &view
				}
				expectedKeys = []string{"activity", "batches", "checkpoint", "history",
					"lastAppliedEventSequenceNumber", "runId", "runtime", "schemaVersion"}
			} else {
				loaded, err := app.store.codecs.UnmarshalRunState([]byte(result.Encoded))
				if !assert.NoError(t, err) || !assert.Equal(t, gent.RunStateReady, loaded.Status) {
					return
				}
				assert.Equal(t, before.head, loaded.State.LastAppliedEventSequenceNumber())
				full, err := app.store.load(last.RunId, before, replayBatches)
				assert.NoError(t, err)
				encoded, err := app.store.codecs.MarshalRunState(full)
				assert.NoError(t, err)
				assert.Equal(t, encoded, []byte(result.Encoded))
				recovered = loaded.State
				expectedKeys = []string{"Checkpoint", "EventSequenceNumber", "Evidence",
					"History", "HistoryOrder", "HistorySources", "LastBatchId", "LastOutcome",
					"RunId", "Runtime", "SchemaVersion"}
			}
			decoded, err := json.Marshal(recovered)
			assert.NoError(t, err)
			assert.Equal(t, string(decoded), result.Decoded)
			assert.Equal(t, len(decoded), result.DecodedBytes)
			assert.Equal(t, len([]byte(result.Encoded)), result.EncodedBytes)
			// Private RunState fields must not collapse to {} in diagnostic JSON. Compare the
			// complete field set as well as the recovered value and exact UTF-8 byte lengths.
			var fields map[string]json.RawMessage
			assert.NoError(t, json.Unmarshal(decoded, &fields))
			var keys []string
			for key := range fields {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			assert.Equal(t, expectedKeys, keys)
			after, _ := app.store.read(last.RunId)
			assert.Equal(t, before, after)
			assert.Empty(t, app.running)
		})
	}
}

func TestMissingConversationHTTPStatus(t *testing.T) {
	type input struct{ method, path, body string }
	type expected struct {
		status int
		body   string
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	for _, test := range []testCase{
		{"load", input{http.MethodGet, "/api/state?runId=missing", ""},
			expected{404, `{"detail":"Conversation not found."}`}},
		{"restore", input{http.MethodPost, "/api/action",
			`{"action":"restore","runId":"missing"}`},
			expected{404, `{"detail":"Conversation not found."}`}},
		{"new turn", input{http.MethodPost, "/api/action",
			`{"action":"turn","runId":"missing","prompt":"Next task"}`},
			expected{404, `{"detail":"Conversation not found. A server restart clears memory."}`}},
		{"continue", input{http.MethodPost, "/api/action",
			`{"action":"continue","runId":"missing"}`},
			expected{404, `{"detail":"Conversation not found. A server restart clears memory."}`}},
	} {
		t.Run(test.name, func(t *testing.T) {
			app, _ := testApplication(t, 0)
			request := httptest.NewRequest(test.input.method, test.input.path,
				strings.NewReader(test.input.body))
			request.Header.Set(serverHeader, app.serverId)
			response := httptest.NewRecorder()
			app.routes().ServeHTTP(response, request)
			assert.Equal(t, test.expected, expected{response.Code, response.Body.String()})
		})
	}
}

func TestInspectionReportsUnavailableInputs(t *testing.T) {
	type input struct{ query string }
	type expected struct {
		status int
		body   string
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	for _, test := range []testCase{
		{"unknown kind", input{"kind=other&path=capture"},
			expected{400, `{"detail":"Select view or state for inspection."}`}},
		{"unknown path", input{"kind=state&path=other"},
			expected{400, `{"detail":"Select batches or capture for replay."}`}},
		{"missing run", input{"kind=state&path=capture&runId=missing"},
			expected{404, `{"detail":"Conversation not found."}`}},
		{"empty run", input{"kind=state&path=capture&runId=empty"},
			expected{409, `{"detail":"No accepted savepoint is available yet."}`}},
	} {
		t.Run(test.name, func(t *testing.T) {
			app, _ := testApplication(t, 0)
			app.store.claim("empty")
			request := httptest.NewRequest(http.MethodGet, "/api/inspect?"+test.input.query, nil)
			response := httptest.NewRecorder()
			app.routes().ServeHTTP(response, request)
			assert.Equal(t, test.expected, expected{response.Code, response.Body.String()})
		})
	}
}

func TestEveryDisplayedEventHasCompleteTypedEvidence(t *testing.T) {
	app, _ := testApplication(t, 0)
	state := executeAction(t, app, &actionRequest{Action: actionTurn,
		Prompt: "Find John Smith.", Path: replayCapture})
	data, _ := app.store.read(state.RunId)
	view, err := app.store.archive(state.RunId, data)
	assert.NoError(t, err)
	var records []*gent.RecordedEvent
	for _, event := range state.Events {
		var record gent.RecordedEvent
		if !assert.NoError(t, app.store.codecs.Unmarshal([]byte(event.Record), &record)) {
			return
		}
		records = append(records, &record)
	}
	assert.Equal(t, view.Activity, records)
}
