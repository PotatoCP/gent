package multiturntest

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/rickchristie/gent"
	"github.com/stretchr/testify/assert"
)

func TestProviderFirstContentExcludesDeliveryDelay(t *testing.T) {
	type input struct {
		format        string
		blockCallback bool
	}
	type expected struct {
		callbacks int
		err       error
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"stream callback", input{"stream", true}, expected{1, nil}},
		{"JSON callback", input{"json", true}, expected{1, nil}},
		{"bounded delivery", input{"stream", false}, expected{65, nil}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			body, mime := generationInfoFixture(t, "openai", test.input.format)
			if !test.input.blockCallback {
				// The provider buffer holds 64 chunks. Chunk 65 must wait for this test to drain.
				delta := metadataSSE(`{"type":"response.output_text.delta",
"output_index":0,"delta":"Done."}`)
				final := strings.ReplaceAll(metadataResponses, "Done.", strings.Repeat("Done.", 65))
				body = strings.Repeat(delta, 65) + metadataSSE(
					`{"type":"response.completed","response":`+final+`}`)
			}
			client := &http.Client{Transport: responseTransport(func(*http.Request) (
				*http.Response, error,
			) {
				return fixtureHTTP(body, mime), nil
			})}
			first := make(chan time.Duration, 1)
			blocked := make(chan struct{})
			release := make(chan struct{})
			calls := 0
			started := time.Now()
			callback := func(ctx context.Context, _ []byte) error {
				calls++
				if calls == 1 {
					first <- time.Since(started)
				}
				if calls != test.expected.callbacks {
					return nil
				}
				close(blocked)
				if !test.input.blockCallback {
					return nil
				}
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			stream, err := fixtureModel("openai", client).GenerateContentStream(ctx,
				gent.NewModelCallRequest([]*gent.ModelMessage{
					gent.TextParts(gent.ChatMessageTypeHuman, "Finish."),
				}, gent.WithStreamingFunc(callback)))
			if !assert.Equal(t, test.expected.err, err) {
				return
			}
			defer stream.Close()
			select {
			case <-blocked:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			firstBound := <-first
			// Make the delivery delay larger than the complete setup time. A timer measured
			// after callbacks or blocked Send would then exceed the first-content bound.
			delay := 2*firstBound + time.Millisecond
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			close(release)
			for range stream.Chunks() {
			}
			response, err := stream.Response()
			if !assert.Equal(t, test.expected.err, err) || !assert.NotNil(t, response) {
				return
			}
			assert.Equal(t, test.expected.callbacks, calls)
			assert.GreaterOrEqual(t, response.Info.TimeToFirstContent, time.Nanosecond)
			assert.LessOrEqual(t, response.Info.TimeToFirstContent, firstBound)
			assert.GreaterOrEqual(t, response.Info.Duration, delay)
		})
	}
}
