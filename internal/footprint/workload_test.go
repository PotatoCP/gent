package footprint_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/golang/snappy"
	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/agents/react"
	"github.com/rickchristie/gent/executor"
	"github.com/rickchristie/gent/toolchain"
)

// This fixture uses real ReAct, managed tool calls, streaming, saves, and replay. The provider
// supplies fixed business content without keeping requests. This removes network and provider
// cost from the measurement and avoids a mock's request archive becoming part of run memory.
type businessModel struct{}

func (*businessModel) PrepareRequest(execCtx *gent.ExecutionContext,
	request *gent.ModelCallRequest, chain gent.ToolChain,
) (*gent.ModelCallRequest, error) {
	return gent.PrepareModelRequest(execCtx, request, chain)
}

func (*businessModel) GenerateContentStream(
	_ context.Context, request *gent.ModelCallRequest,
) (gent.Stream, error) {
	info := &gent.GenerationInfo{InputTokens: 2048, OutputTokens: 256}
	if request.Messages[len(request.Messages)-1].Role != gent.ChatMessageTypeTool {
		return gent.NewCompletedStream(&gent.ModelCallResponse{
			Info: info, Choices: []*gent.ContentChoice{{
				Completion: gent.ModelCompletion{Status: gent.ModelStatusToolCalls},
				Parts: []gent.ContentPart{&gent.ModelToolCallRequest{
					ID: fmt.Sprintf("lookup-%d", len(request.Messages)), Type: "function",
					FunctionCall: &gent.FunctionCall{Name: "lookup", Arguments: `{"id":"PO-1042"}`},
				}},
			}},
		}, nil), nil
	}
	stream := gent.NewStreamWithDuration()
	answer := businessAnswer()
	for index := range 100 {
		text := answer[index*len(answer)/100 : (index+1)*len(answer)/100]
		stream.Send(gent.StreamChunk{Content: []gent.ContentPart{&gent.AssistantContent{
			Type: gent.AssistantContentFinalAnswerCandidate, MessageId: "answer",
			Content: []gent.ContentPart{gent.TextPart(text)},
		}}})
	}
	stream.Complete(&gent.ModelCallResponse{Info: info, Choices: []*gent.ContentChoice{{
		Completion: gent.ModelCompletion{Status: gent.ModelStatusAnswer},
		Parts: []gent.ContentPart{&gent.AssistantContent{
			Type: gent.AssistantContentFinalAnswerCandidate, MessageId: "answer",
			Content: []gent.ContentPart{gent.TextPart(answer)},
		}}, CandidateSource: &gent.ContentSource{MessageIds: []string{"answer"}},
	}}}, nil)
	return stream, nil
}

type businessInput struct {
	ID string `json:"id"`
}

func businessAnswer() string {
	var report strings.Builder
	report.WriteString("Purchase order PO-1042 is approved. The supplier confirmed these lines. ")
	for index := range 24 {
		fmt.Fprintf(&report, "SKU P-%04d: %d units, delivery in %d days. ",
			1042+index, 3+index*7%29, 1+index%5)
	}
	report.WriteString("Lines with a five-day delivery need an operations review. " +
		"Keep the purchase order open until the receiving team confirms all quantities. " +
		"Send the buyer the delivery schedule and request approval before changing the order.")
	return report.String()
}

func businessOrder(id string) string {
	var record strings.Builder
	fmt.Fprintf(&record, "Order %s; buyer: Operations; approved: 2026-11-18; currency: USD. ", id)
	for index := range 32 {
		fmt.Fprintf(&record,
			"Line %02d; SKU P-%04d; quantity %d; unit price %d.%02d; supplier S-%02d; ETA %d days. ",
			index+1, 1042+index, 3+index*7%29, 11+index*13%83, index*19%100, 1+index%3, 1+index%5)
	}
	return record.String()
}

type businessRecorder struct {
	codecs   *gent.CodecRegistry
	batches  [][]byte
	snapshot []byte
	tail     int
}

func (recorder *businessRecorder) Save(_ context.Context, batch *gent.SaveBatch,
	capture func() (*gent.RunState, error),
) error {
	data, err := recorder.codecs.Marshal(batch)
	if err != nil {
		return err
	}
	// Keep one periodic RunStateSnapshot. The accepted tail still has to be replayed.
	if len(recorder.batches)%4 == 0 {
		state, err := capture()
		if err != nil {
			return err
		}
		recorder.snapshot, err = recorder.codecs.MarshalRunState(state)
		if err != nil {
			return err
		}
		recorder.tail = len(recorder.batches) + 1
	}
	recorder.batches = append(recorder.batches, data)
	return nil
}

func businessRunner(recorder *businessRecorder) *executor.Runner[string] {
	return configureBusiness(&businessModel{}, recorder.codecs, recorder)
}

func configureBusiness(model gent.Model, codecs *gent.CodecRegistry,
	recorder gent.Recorder,
) *executor.Runner[string] {
	chain := toolchain.New()
	chain.RegisterTool(gent.DefineTool("lookup",
		func(_ context.Context, input *businessInput) (*gent.ToolExecutionResponse, error) {
			return &gent.ToolExecutionResponse{Status: gent.ToolExecuted,
				Parts: []gent.ContentPart{gent.TextPart(businessOrder(input.ID))}}, nil
		}).Description("Look up the current purchase order and delivery facts.").MustBuild())
	return executor.NewRunner(react.NewAgent(model), &gent.Config{
		Codecs: codecs, Recorder: recorder, ToolChain: chain,
	})
}

func runBusiness(b testing.TB, turns int, options ...gent.CodecOption) (string, *businessRecorder) {
	b.Helper()
	recorder := &businessRecorder{codecs: gent.NewCodecRegistry(options...)}
	runner := businessRunner(recorder)
	execCtx, err := runner.NewExecutionContext(context.Background(), "procurement")
	if err != nil {
		b.Fatal(err)
	}
	defer execCtx.Release()
	for turn := range turns {
		task := gent.NewTask(gent.TextPart(fmt.Sprintf(
			"Review purchase order PO-1042, follow-up %d. Check supplier and delivery facts.", turn)))
		answer, result, err := runner.NewTurn(execCtx, task)
		if err != nil || result.Status != gent.ExecutionFinished || answer == "" {
			b.Fatalf("turn %d: result %v, error %v", turn, result, err)
		}
	}
	return execCtx.RunId(), recorder
}

func BenchmarkBusinessPersistence(b *testing.B) {
	for _, turns := range []int{1, 10} {
		b.Run(fmt.Sprintf("turns_%d", turns), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_, recorder := runBusiness(b, turns)
				size := 0
				for _, data := range recorder.batches {
					size += len(data)
				}
				b.ReportMetric(float64(size), "batch-B")
				b.ReportMetric(float64(len(recorder.snapshot)), "snapshot-B")
				b.ReportMetric(float64(len(recorder.batches)), "saves")
			}
		})
	}
}

func BenchmarkBusinessStorage(b *testing.B) {
	runID, recorder := runBusiness(b, 10)
	view := gent.NewRunView(runID)
	for _, data := range recorder.batches {
		var batch gent.SaveBatch
		if err := recorder.codecs.Unmarshal(data, &batch); err != nil {
			b.Fatal(err)
		}
		result, err := view.Apply(&batch, recorder.codecs)
		if err != nil || result.Status != gent.ApplyApplied {
			b.Fatalf("archive: result %v, error %v", result, err)
		}
	}
	archive, err := recorder.codecs.Marshal(view)
	if err != nil {
		b.Fatal(err)
	}
	type input struct {
		name string
		data [][]byte
	}
	for _, item := range []input{
		{"batches", recorder.batches}, {"snapshot", [][]byte{recorder.snapshot}},
		{"archive", [][]byte{archive}},
	} {
		b.Run(item.name, func(b *testing.B) {
			type format struct {
				name   string
				encode func([]byte) []byte
			}
			var buffer bytes.Buffer
			writer, err := gzip.NewWriterLevel(&buffer, gzip.BestSpeed)
			if err != nil {
				b.Fatal(err)
			}
			for _, format := range []format{
				{"json", func(data []byte) []byte { return data }},
				{"snappy", func(data []byte) []byte { return snappy.Encode(nil, data) }},
				{"gzip_fast", func(data []byte) []byte {
					buffer.Reset()
					writer.Reset(&buffer)
					if _, err := writer.Write(data); err != nil {
						b.Fatal(err)
					}
					if err := writer.Close(); err != nil {
						b.Fatal(err)
					}
					return buffer.Bytes()
				}},
			} {
				b.Run(format.name, func(b *testing.B) {
					b.ReportAllocs()
					for b.Loop() {
						size := 0
						for _, data := range item.data {
							size += len(format.encode(data))
						}
						b.ReportMetric(float64(size), "stored-B")
					}
				})
			}
		})
	}
}
