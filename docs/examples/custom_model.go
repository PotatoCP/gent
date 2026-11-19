package examples

import (
	"context"

	"github.com/rickchristie/gent"
)

// docs:start custom-model-prepare
type FixedModel struct {
	Text string
}

func (model *FixedModel) PrepareRequest(execCtx *gent.ExecutionContext,
	request *gent.ModelCallRequest, chain gent.ToolChain,
) (*gent.ModelCallRequest, error) {
	return gent.PrepareModelRequest(execCtx, request, chain)
}

// docs:end custom-model-prepare

// docs:start custom-model-response
func (model *FixedModel) GenerateContentStream(ctx context.Context,
	request *gent.ModelCallRequest,
) (gent.Stream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	response := &gent.ModelCallResponse{Choices: []*gent.ContentChoice{{
		Completion:      gent.ModelCompletion{Status: gent.ModelStatusAnswer},
		CandidateSource: &gent.ContentSource{MessageIds: []string{"answer"}},
		Parts: []gent.ContentPart{&gent.AssistantContent{
			Type:      gent.AssistantContentFinalAnswerCandidate,
			MessageId: "answer",
			Content:   []gent.ContentPart{gent.TextPart(model.Text)},
		}},
	}}}
	// A candidate is not yet an accepted answer. Output processing follows this response.
	return gent.NewCompletedStream(response, nil), nil
}

// docs:end custom-model-response

func streamExample(ctx context.Context) (gent.Stream, error) {
	// docs:start custom-model-stream
	workerCtx, cancel := context.WithCancel(ctx)
	stream, err := gent.NewBoundedStreamWithDuration(workerCtx, 16, cancel)
	if err != nil {
		cancel()
		return nil, err
	}
	go func() {
		// A real adapter closes its response body and joins producers before Complete.
		response, callErr := (&FixedModel{Text: "Ready."}).GenerateContentStream(workerCtx, nil)
		if callErr != nil {
			cancel()
			stream.Complete(nil, callErr)
			return
		}
		final, callErr := response.Response()
		if callErr == nil {
			stream.Send(gent.StreamChunk{Content: final.Choices[0].Parts})
		}
		cancel()
		stream.Complete(final, callErr)
	}()
	return stream, nil
	// docs:end custom-model-stream
}
