package gent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestBoundedProviderStream_CancellationReleasesBackpressure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := NewBoundedStreamWithDuration(ctx, 1, nil)
	assert.NoError(t, err)
	assert.True(t, stream.Send(StreamChunk{Content: []ContentPart{&AssistantContent{
		Type:    AssistantContentIntermediary,
		Content: []ContentPart{TextContent{Text: "first"}}, MessageId: "first",
	}}}))
	started := make(chan struct{})
	done := make(chan bool, 1)
	go func() {
		close(started)
		done <- stream.Send(
			StreamChunk{
				Content: []ContentPart{
					&AssistantContent{

						Type: AssistantContentIntermediary,
						Content: []ContentPart{
							TextContent{
								Text: "second",
							},
						},
						MessageId: "second",
					},
				},
			},
		)
	}()
	<-started
	select {
	case <-done:
		t.Fatal("bounded queue did not apply backpressure")
	case <-time.After(10 * time.Millisecond):
	}
	cancel()
	select {
	case sent := <-done:
		assert.False(t, sent)
	case <-time.After(time.Second):
		t.Fatal("cancellation did not release producer")
	}
	stream.Complete(nil, context.Canceled)
	_, err = stream.Response()
	assert.ErrorIs(t, err, context.Canceled)
}
