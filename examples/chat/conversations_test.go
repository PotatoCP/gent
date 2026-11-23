package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rickchristie/gent"
	"github.com/stretchr/testify/assert"
)

func httpRequest(app *application, method, path, body, serverId string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set(serverHeader, serverId)
	response := httptest.NewRecorder()
	app.routes().ServeHTTP(response, request)
	return response
}

func submitHTTP(t *testing.T, app *application, input *actionRequest) string {
	t.Helper()
	body, err := json.Marshal(input)
	if !assert.NoError(t, err) {
		t.FailNow()
	}
	response := httpRequest(app, "POST", "/api/action", string(body), app.serverId)
	if !assert.Equal(t, http.StatusAccepted, response.Code, response.Body.String()) {
		t.FailNow()
	}
	var result struct {
		RunId string `json:"runId"`
	}
	if !assert.NoError(t, json.Unmarshal(response.Body.Bytes(), &result)) {
		t.FailNow()
	}
	return result.RunId
}

// The browser reads display data, not Go interface values. Decode only the fields used here;
// recovery tests use CodecRegistry to reconstruct typed content from saved bytes.
type savedChatResponse struct {
	RunId      string          `json:"runId"`
	LastAnswer string          `json:"lastAnswer"`
	Events     []*displayEvent `json:"events"`
	Outcome    *attemptOutcome `json:"outcome"`
}

func savedHTTP(t *testing.T, app *application, runId string) *savedChatResponse {
	t.Helper()
	app.wait.Wait()
	response := httpRequest(app, "GET", "/api/state?runId="+runId, "", "")
	var state savedChatResponse
	if !assert.Equal(t, 200, response.Code) ||
		!assert.NoError(t, json.Unmarshal(response.Body.Bytes(), &state)) {
		t.FailNow()
	}
	return &state
}

func TestConversationListAndContinuationHTTP(t *testing.T) {
	app, _ := testApplication(t, 0)
	started := time.Now()
	first := submitHTTP(t, app, &actionRequest{Action: actionTurn, Prompt: "Remember Cedar."})
	savedHTTP(t, app, first)
	second := submitHTTP(t, app, &actionRequest{Action: actionTurn, Scenario: scenarioEcommerce,
		Prompt: "Check my order."})
	savedHTTP(t, app, second)
	continued := submitHTTP(t, app, &actionRequest{Action: actionTurn, RunId: first,
		Prompt: "What did I ask first?"})
	state := savedHTTP(t, app, continued)
	assert.Equal(t, first, continued)
	assert.Equal(t, "Your first message was: Remember Cedar. Earlier lookup: "+
		"I found John Smith (C001), using john.smith@email.com.", state.LastAnswer)
	response := httpRequest(app, "GET", "/api/conversations", "", "")
	var chats []conversationInfo
	assert.Equal(t, 200, response.Code)
	assert.NoError(t, json.Unmarshal(response.Body.Bytes(), &chats))
	if !assert.Len(t, chats, 2) {
		return
	}
	for _, chat := range chats {
		assert.GreaterOrEqual(t, chat.UpdatedAt, started)
	}
	assert.Equal(t, []conversationInfo{
		{RunId: first, Scenario: scenarioAirline, Title: "Remember Cedar.",
			UpdatedAt: chats[0].UpdatedAt, Status: gent.ExecutionFinished},
		{RunId: second, Scenario: scenarioEcommerce, Title: "Check my order.",
			UpdatedAt: chats[1].UpdatedAt, Status: gent.ExecutionFinished},
	}, chats)
}

func TestServerResetClearsChatsAndExternalFixtures(t *testing.T) {
	app, _ := testApplication(t, 0)
	app.model = func() gent.Model { return &bookingModel{} }
	first := submitHTTP(t, app, &actionRequest{Action: actionTurn, Prompt: "Cancel BK001"})
	assert.Equal(t, "Booking BK001 cancelled. Travel credit of $199.00 issued.",
		savedHTTP(t, app, first).LastAnswer)
	queue, unsubscribe := app.hub.subscribe(first)
	defer unsubscribe()
	assert.NoError(t, app.hub.publish(first, &browserFrame{Kind: frameHeartbeat}))
	oldServer := app.serverId
	response := httpRequest(app, "POST", "/api/reset", "{}", oldServer)
	assert.Equal(t, 200, response.Code)
	assert.Equal(t, `{"detail":"Server reset. All chats were cleared."}`, response.Body.String())
	assert.NotEqual(t, oldServer, response.Header().Get(serverHeader))
	_, open := <-queue
	assert.False(t, open)
	assert.Empty(t, app.store.values)
	assert.Empty(t, app.store.runs)
	assert.Empty(t, app.sessions)
	assert.Empty(t, app.hub.rooms)
	assert.Empty(t, app.outcomes)
	assert.Empty(t, app.cancels)
	assert.Empty(t, app.running)
	assert.Equal(t, `[]`, httpRequest(app, "GET", "/api/conversations", "", "").Body.String())
	assert.Equal(t, 404, httpRequest(app, "GET", "/api/state?runId="+first, "", "").Code)
	assert.Equal(t, 409, httpRequest(app, "POST", "/api/action",
		`{"action":"turn","prompt":"Late first prompt"}`, oldServer).Code)
	assert.Empty(t, app.chats)
	fresh := submitHTTP(t, app, &actionRequest{Action: actionTurn, Prompt: "Read BK001"})
	assert.Equal(t, "confirmed", savedHTTP(t, app, fresh).LastAnswer)
	assert.NotEqual(t, first, fresh)
	// Repeated reset remains valid when the caller sends the current server identity.
	assert.Equal(t, 200, httpRequest(app, "POST", "/api/reset", "{}", app.serverId).Code)
}

type resetModel struct {
	demoModel
	entered, cancelled, finish chan struct{}
}

func (model *resetModel) GenerateContentStream(ctx context.Context, _ *gent.ModelCallRequest) (
	gent.Stream, error,
) {
	close(model.entered)
	<-ctx.Done()
	close(model.cancelled)
	<-model.finish
	return nil, ctx.Err()
}

func waitSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(10 * time.Second):
		t.Fatal("worker signal did not arrive")
	}
}

func TestServerResetJoinsWorkersAndRejectsOldAdmission(t *testing.T) {
	app, _ := testApplication(t, 0)
	model := &resetModel{entered: make(chan struct{}), cancelled: make(chan struct{}),
		finish: make(chan struct{})}
	defer close(model.finish) // Release the test barrier even if an assertion stops the test.
	app.model = func() gent.Model { return model }
	submitHTTP(t, app, &actionRequest{Action: actionTurn, Prompt: "Wait for reset."})
	waitSignal(t, model.entered)
	oldServer := app.serverId
	resetDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { resetDone <- httpRequest(app, "POST", "/api/reset", "{}", oldServer) }()
	waitSignal(t, model.cancelled)
	select {
	case <-resetDone:
		t.Fatal("reset returned before worker cleanup")
	default:
	}
	lateDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		lateDone <- httpRequest(app, "POST", "/api/action",
			`{"action":"turn","prompt":"Stale first prompt"}`, oldServer)
	}()
	model.finish <- struct{}{}
	assert.Equal(t, 200, (<-resetDone).Code)
	assert.Equal(t, 409, (<-lateDone).Code)
	assert.Empty(t, app.conversations())
	assert.Empty(t, app.store.values)
	assert.Empty(t, app.hub.rooms)
}

func TestConversationTitle(t *testing.T) {
	type input struct{ prompt string }
	type expected struct{ title string }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	for _, test := range []testCase{
		{"spaces", input{`  Check   my
booking.  `}, expected{"Check my booking."}},
		{"unicode", input{strings.Repeat("旅", 81)}, expected{strings.Repeat("旅", 80) + "…"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.expected, expected{conversationTitle(test.input.prompt)})
		})
	}
}
