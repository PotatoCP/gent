package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/integrationtest/airline"
	"github.com/rickchristie/gent/integrationtest/ecommerce"
	"github.com/stretchr/testify/assert"
)

func TestScenariosUseSharedTasksAndRestoreBothPaths(t *testing.T) {
	type input struct {
		id   scenarioId
		path replayPath
	}
	type expected struct{ prompt, answer string }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	for _, test := range []testCase{
		{"airline batches", input{scenarioAirline, replayBatches}, expected{airline.CustomerRequest,
			"I found John Smith (C001), using john.smith@email.com."}},
		{"airline capture", input{scenarioAirline, replayCapture}, expected{airline.CustomerRequest,
			"I found John Smith (C001), using john.smith@email.com."}},
		{"ecommerce batches", input{scenarioEcommerce, replayBatches}, expected{ecommerce.CustomerRequest,
			"I found Alex Rivera (C001), using alex.rivera@email.com."}},
		{"ecommerce capture", input{scenarioEcommerce, replayCapture}, expected{ecommerce.CustomerRequest,
			"I found Alex Rivera (C001), using alex.rivera@email.com."}},
	} {
		t.Run(test.name, func(t *testing.T) {
			app, _ := testApplication(t, 0)
			first := executeAction(t, app, &actionRequest{Action: actionTurn, Scenario: test.input.id,
				Prompt: test.expected.prompt, Path: test.input.path})
			assert.Equal(t, test.expected.answer, first.LastAnswer)
			assert.Equal(t, test.input.id, first.Scenario)
			session := app.sessions[first.RunId]
			second := executeAction(t, app, &actionRequest{Action: actionTurn, RunId: first.RunId,
				Prompt: "Remember the first request.", Path: test.input.path})
			assert.Same(t, session, app.sessions[first.RunId])
			assert.Equal(t, first.RunId, second.RunId)
			assert.Equal(t, test.input.id, second.Scenario)
			assert.Equal(t, "Your first message was: "+test.expected.prompt+" Earlier lookup: "+
				test.expected.answer, second.LastAnswer)
			data, ok := app.store.read(first.RunId)
			assert.True(t, ok)
			full, err := app.store.load(first.RunId, data, replayBatches)
			assert.NoError(t, err)
			captured, err := app.store.load(first.RunId, data, replayCapture)
			assert.NoError(t, err)
			fullBytes, err := app.store.codecs.MarshalRunState(full)
			assert.NoError(t, err)
			capturedBytes, err := app.store.codecs.MarshalRunState(captured)
			assert.NoError(t, err)
			assert.Equal(t, fullBytes, capturedBytes)
		})
	}
}

func TestScenarioSelectionHTTP(t *testing.T) {
	type input struct {
		body     string
		existing bool
	}
	type expected struct {
		status int
		detail string
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	for _, test := range []testCase{
		{"unknown scenario", input{`{"action":"turn","prompt":"Hello","scenario":"unknown"}`, false},
			expected{400, "Choose an airline or e-commerce scenario."}},
		{"change saved scenario",
			input{`{"action":"turn","prompt":"Hello","scenario":"ecommerce"}`, true},
			expected{409, "Start a new chat to change the scenario."}},
	} {
		t.Run(test.name, func(t *testing.T) {
			app, _ := testApplication(t, 0)
			var action actionRequest
			assert.NoError(t, json.Unmarshal([]byte(test.input.body), &action))
			if test.input.existing {
				state := executeAction(t, app, &actionRequest{Action: actionTurn,
					Scenario: scenarioAirline, Prompt: airline.CustomerRequest, Path: replayCapture})
				action.RunId = state.RunId
			}
			_, status, err := app.start(&action)
			assert.Equal(t, test.expected.status, status)
			assert.EqualError(t, err, test.expected.detail)
		})
	}
	app, _ := testApplication(t, 0)
	response := httptest.NewRecorder()
	app.routes().ServeHTTP(response, httptest.NewRequest("GET", "/api/config", nil))
	var actual struct {
		Provider  string         `json:"provider"`
		Scenarios []scenarioInfo `json:"scenarios"`
	}
	assert.Equal(t, 200, response.Code)
	assert.NoError(t, json.Unmarshal(response.Body.Bytes(), &actual))
	assert.Equal(t, "demo", actual.Provider)
	assert.Equal(t, []scenarioInfo{
		{scenarioAirline, "SkyWings Airlines", "Change a flight booking", airline.CustomerRequest},
		{scenarioEcommerce, "TechGear Store", "Investigate a duplicate charge",
			ecommerce.CustomerRequest},
	}, actual.Scenarios)
}

// Use actual booking mutations, not private fixture fields, to prove that worker recovery keeps
// the external service and that a new conversation receives an independent service.
func TestScenarioServiceSurvivesWorkerRestoreAndIsolatesConversations(t *testing.T) {
	type input struct{ path replayPath }
	type expected struct{ existing, fresh string }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	for _, test := range []testCase{
		{"all batches", input{replayBatches}, expected{"cancelled", "confirmed"}},
		{"capture and tail", input{replayCapture}, expected{"cancelled", "confirmed"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			app, _ := testApplication(t, 0)
			app.model = func() gent.Model { return &bookingModel{} }
			first := executeAction(t, app, &actionRequest{Action: actionTurn, Scenario: scenarioAirline,
				Prompt: "Cancel BK001", Path: test.input.path})
			assert.Equal(t, "Booking BK001 cancelled. Travel credit of $199.00 issued.", first.LastAnswer)
			second := executeAction(t, app, &actionRequest{Action: actionTurn, RunId: first.RunId,
				Prompt: "Read BK001", Path: test.input.path})
			fresh := executeAction(t, app, &actionRequest{Action: actionTurn, Scenario: scenarioAirline,
				Prompt: "Read BK001", Path: test.input.path})
			assert.Equal(t, test.expected, expected{second.LastAnswer, fresh.LastAnswer})
		})
	}
}

type bookingModel struct{ demoModel }

func (model *bookingModel) GenerateContentStream(
	ctx context.Context, request *gent.ModelCallRequest,
) (
	gent.Stream, error,
) {
	var task, answer string
	for _, message := range request.Messages {
		if message.Role == gent.ChatMessageTypeHuman {
			task, answer = gent.ContentText(message.Parts), ""
		}
		if message.Role != gent.ChatMessageTypeTool {
			continue
		}
		for _, part := range message.Parts {
			reply, ok := part.(*gent.ModelToolCallResponse)
			if !ok {
				continue
			}
			for _, part := range reply.Parts() {
				if data, ok := part.(gent.DataContent); ok {
					fields := data.Value().(map[string]any)
					answer, _ = fields["status"].(string)
					if answer == "" {
						answer, _ = fields["message"].(string)
					}
				}
			}
		}
	}
	if answer != "" {
		return model.streamAnswer(ctx, answer)
	}
	name := "get_booking_info"
	if task == "Cancel BK001" {
		name = "cancel_booking"
	}
	return gent.NewCompletedStream(&gent.ModelCallResponse{Choices: []*gent.ContentChoice{{
		Completion: gent.ModelCompletion{Status: gent.ModelStatusToolCalls},
		Parts: []gent.ContentPart{&gent.ModelToolCallRequest{ID: "booking", Type: "function",
			FunctionCall: &gent.FunctionCall{Name: name, Arguments: `{"booking_id":"BK001"}`}}},
	}}}, nil), nil
}
