package models

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/output"
	"github.com/stretchr/testify/assert"
)

func TestLiveProviderHistory(t *testing.T) {
	for _, provider := range liveProviders() {
		t.Run(provider.name, func(t *testing.T) {
			config := liveConfig(t, provider, false)
			t.Parallel()
			model := newLiveModel(t, provider, &config)
			t.Run("AssistantHistory", func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				checkLiveAssistantHistory(t, ctx, model)
			})
			t.Run("RepeatedCompaction", func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
				defer cancel()
				secret := make([]byte, 12)
				if _, err := rand.Read(secret); !assert.NoError(t, err) {
					return
				}
				checkProviderCompaction(t, ctx, model, fmt.Sprintf("record-%x", secret))
			})
		})
	}
}

func checkLiveAssistantHistory(t *testing.T, ctx context.Context, model gent.Model) {
	t.Helper()
	type historyAnswer struct {
		Area      string `json:"area"`
		Reference string `json:"reference"`
	}
	// Only the saved assistant message contains the reference. The schema fixes the reply
	// format while leaving both values to be read from history.
	var nonce [4]byte
	if _, err := rand.Read(nonce[:]); !assert.NoError(t, err) {
		return
	}
	want := historyAnswer{Area: "Tebet", Reference: fmt.Sprintf("R-%x", nonce)}
	out, err := output.NewJsonOutput[historyAnswer]()
	if !assert.NoError(t, err) {
		return
	}
	request := gent.NewModelCallRequest([]*gent.ModelMessage{
		gent.TextParts(gent.ChatMessageTypeSystem, out.Instructions()),
		gent.TextParts(gent.ChatMessageTypeHuman, "Keep the following booking summary."),
		gent.TextParts(gent.ChatMessageTypeAI,
			fmt.Sprintf("Booking summary. Area: %s. Reference: %s.", want.Area, want.Reference)),
		gent.TextParts(gent.ChatMessageTypeHuman,
			"Return the area name and the booking reference from the summary. Copy each value exactly."),
	}, gent.WithMaxTokens(2048))
	if !assert.NoError(t, out.Configure(request)) {
		return
	}
	stream, err := model.GenerateContentStream(ctx, request)
	if !assert.NoError(t, err) {
		return
	}
	defer stream.Close()
	for range stream.Chunks() {
	}
	response, err := stream.Response()
	if !assert.NoError(t, err) || !assert.Len(t, response.Choices, 1) {
		return
	}
	assert.Equal(t, gent.ModelStatusAnswer, response.Choices[0].Completion.Status)
	encoded, err := json.Marshal(want)
	if assert.NoError(t, err) {
		assert.JSONEq(t, string(encoded), gent.ContentText(response.Choices[0].CandidateContent()))
	}
}
