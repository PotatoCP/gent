package gent_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/agents/react"
	"github.com/rickchristie/gent/output"
	"github.com/stretchr/testify/assert"
)

func TestCheckpointTypedImageWaitsForSaveAndRestores(t *testing.T) {
	type input struct{ loseAcknowledgment bool }
	type expected struct {
		published bool
		err       error
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	type execution struct {
		value  []gent.BinaryContent
		result *gent.ExecutionResult
		err    error
	}
	var imageFile bytes.Buffer
	if !assert.NoError(t, png.Encode(&imageFile, image.NewRGBA(image.Rect(0, 0, 2, 2)))) {
		return
	}
	imagePart := gent.BinaryContent{MIMEType: "image/png", Data: imageFile.Bytes()}
	wantValue, wantContent := []gent.BinaryContent{imagePart}, []gent.ContentPart{imagePart}
	failure := errors.New("terminal commit acknowledgment lost")
	for _, test := range []testCase{
		{"commit", input{}, expected{true, nil}},
		{"lost acknowledgment", input{true}, expected{false, failure}},
	} {
		t.Run(
			test.name,
			func(t *testing.T) {
				store := &contentStore{codecs: gent.NewCodecRegistry(), reached: make(chan struct{}),
					release: make(chan struct{}), accept: true}
				if test.input.loseAcknowledgment {
					store.err = failure
				}
				unblock := sync.OnceFunc(func() { close(store.release) })
				defer unblock()
				previews := make(chan *gent.StreamChunk, 1)
				observer := &contentObserver{before: func(_ context.Context, event *gent.RecordedEvent) error {
					if event.Type == "model_stream_chunk" {
						previews <- event.Payload.(*gent.StreamChunk)
					}
					return nil
				}}
				var modelCalls, processed, validated atomic.Int64
				model := checkpointModel(func(context.Context, *gent.ModelCallRequest) (gent.Stream, error) {
					modelCalls.Add(1)
					stream := gent.NewStreamWithDuration()
					item := &gent.AssistantContent{Type: gent.AssistantContentFinalAnswerCandidate,
						MessageId: "image", Content: wantContent}
					stream.Send(gent.StreamChunk{Content: []gent.ContentPart{item}})
					stream.Complete(&gent.ModelCallResponse{Choices: []*gent.ContentChoice{{
						Completion:      gent.ModelCompletion{Status: gent.ModelStatusAnswer},
						CandidateSource: &gent.ContentSource{MessageIds: []string{"image"}},
						Parts:           []gent.ContentPart{item}}}}, nil)
					return stream, nil
				})
				out := output.NewImageOutput().SetCustomValidator("image/v1", func(
					*gent.ExecutionContext, []gent.BinaryContent,
				) (*gent.OutputValidationResult, error) {
					validated.Add(1)
					return &gent.OutputValidationResult{Accepted: true}, nil
				})
				loop := react.NewWithOutput(
					model,
					observedOutput[[]gent.BinaryContent]{
						Output: out,
						calls:  &processed,
					},
				)
				runner, execCtx := newTestExecution(
					t,
					context.Background(),
					loop,
					&gent.Config{
						Codecs:   store.codecs,
						Recorder: store,
						Subscribers: []*gent.SubscriberRegistration{
							{Subscriber: observer, Delivery: gent.SubscriberLive}},
					},
				)
				done := make(chan execution, 1)
				go func() {
					value, result, err := runner.NewTurn(execCtx,
						gent.NewTask(gent.TextPart("Draw a small image.")))
					done <- execution{value, result, err}
				}()
				checkpointSignal(t, store.reached)
				preview := checkpointReceive(t, previews)
				assert.Equal(
					t,
					wantContent,
					gent.AssistantParts(
						preview.Content,
						gent.AssistantContentFinalAnswerCandidate,
					),
				)
				assert.Empty(t, acceptedContent(observer.recorded()))
				select {
				case result := <-done:
					t.Fatalf("typed output returned before save: %v", result)
				default:
				}
				unblock()
				completed := checkpointReceive(t, done)
				assert.ErrorIs(t, completed.err, test.expected.err)
				wantResult := &gent.ExecutionResult{Status: gent.ExecutionFinished, TurnId: execCtx.TurnId(),
					TerminationReason: gent.TerminationSuccess, Output: wantContent}
				published := acceptedContent(observer.recorded())
				if test.expected.published {
					assert.Equal(t, wantValue, completed.value)
					assert.Equal(t, wantResult, completed.result)
					if assert.Len(t, published, 1) {
						assert.Equal(
							t,
							wantContent,
							gent.AssistantParts(
								published[0].Content,
								gent.AssistantContentFinalAnswer,
							),
						)
					}
				} else {
					assert.Nil(t, completed.value)
					assert.Nil(t, completed.result)
					assert.Empty(t, published)
				}
				freshRunner, fresh := restoreTestExecution(
					t,
					loop,
					&gent.Config{
						Codecs: store.codecs,
					},
					store.runStateSnapshot,
				)
				value, result, err := freshRunner.GetLastTurnResult(fresh)
				assert.NoError(t, err)
				assert.Equal(t, wantValue, value)
				assert.Equal(t, wantResult, result)
				assert.Equal(t, int64(1), modelCalls.Load())
				assert.Equal(t, int64(1), processed.Load())
				assert.Equal(t, int64(1), validated.Load())
			},
		)
	}
}
