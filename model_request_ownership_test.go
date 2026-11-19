package gent_test

import (
	"context"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/internal/tt"
	"github.com/stretchr/testify/assert"
)

func TestManagedRequestCopiesMediaOnlyForMutableHooks(t *testing.T) {
	type input struct{ hooks *gent.Hooks }
	type expected struct {
		shared bool
		bytes  []byte
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	cases := []testCase{
		{"no hooks", input{}, expected{true, []byte{1, 2, 3}}},
		{"empty hooks", input{&gent.Hooks{}}, expected{true, []byte{1, 2, 3}}},
		{"tool hook only", input{&gent.Hooks{BeforeToolCall: func(context.Context,
			*gent.BeforeToolCallEvent) error {
			return nil
		}}},
			expected{true, []byte{1, 2, 3}}},
		{"mutable request hook", input{&gent.Hooks{BeforeModelCall: func(_ context.Context,
			event *gent.BeforeModelCallEvent) error {
			event.Request.Messages[0].Parts[0].(gent.BinaryContent).Data[0] = 9
			return nil
		}}},
			expected{false, []byte{9, 2, 3}}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			data := []byte{1, 2, 3}
			execCtx := tt.NewContext(t, &gent.Config{Hooks: test.input.hooks})
			var actual expected
			model := checkpointModel(func(_ context.Context, request *gent.ModelCallRequest) (
				gent.Stream, error,
			) {
				received := request.Messages[0].Parts[0].(gent.BinaryContent).Data
				actual = expected{shared: &received[0] == &data[0], bytes: received}
				return gent.NewCompletedStream(&gent.ModelCallResponse{}, nil), nil
			})
			request := gent.NewModelCallRequest([]*gent.ModelMessage{
				{Role: gent.ChatMessageTypeHuman, Parts: []gent.ContentPart{gent.PDFPart(data)}},
			})
			stream, err := execCtx.GenerateContentStream(model, gent.ModelCall{}, request)
			if !assert.NoError(t, err) {
				return
			}
			_, err = stream.Response()
			stream.Close()
			assert.NoError(t, err)
			assert.Equal(t, test.expected, actual)
			assert.Equal(t, []byte{1, 2, 3}, data)
		})
	}
}
