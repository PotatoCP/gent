package gent

import (
	"context"
	"testing"
)

// Record a full answer stream through the acceptance path. Include digest creation and saved
// events so an optimization cannot omit storage work when there is no listener.
func BenchmarkRecordAnswerStream(b *testing.B) {
	loop := &turnTestLoop{next: func(execCtx *ExecutionContext) (*AgentLoopResult, error) {
		for range 1000 {
			if err := execCtx.EmitChunk(StreamChunk{Content: []ContentPart{&AssistantContent{
				Type: AssistantContentIntermediary, MessageId: "progress",
				Content: []ContentPart{TextPart("word")},
			}}}); err != nil {
				return nil, err
			}
		}
		return acceptTurnText(execCtx, "Done")
	}}
	runner := NewRunner[string](loop, &Config{})
	for b.Loop() {
		execCtx, err := runner.NewExecutionContext(context.Background(), "benchmark")
		if err != nil {
			b.Fatal(err)
		}
		_, _, err = runner.NewTurn(execCtx, NewTask(TextPart("Complete the work.")))
		execCtx.Release()
		if err != nil {
			b.Fatal(err)
		}
	}
	b.ReportAllocs()
}
