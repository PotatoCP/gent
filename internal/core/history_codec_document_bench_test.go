package gent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// Compare complete storage documents with the former typed envelope on the same accepted data.
// Both paths use the current type codecs; the envelope baseline has no tables or sharing.
// The fixture uses NewTurn and Recorder.Save, so duplicated archive and compact state data come
// from normal execution. The decode benchmarks include RunView's saved reducer validation.
func BenchmarkSavedDocument(b *testing.B) {
	type input struct {
		parts    []ContentPart
		metadata any
	}
	type benchmarkCase struct {
		name  string
		input input
	}
	native := &ProviderContinuation{Provider: "fixture", Items: []json.RawMessage{
		json.RawMessage(` {
  "type": "reasoning", "signature": "\u0061<&>", "id": 18446744073709551615
} `),
	}}
	cases := []benchmarkCase{
		{"text", input{parts: []ContentPart{TextPart(strings.Repeat("Keep the task. ", 256))}}},
		{"native", input{metadata: native}},
		{"media", input{parts: []ContentPart{
			BinaryContent{MIMEType: "image/png", Data: bytes.Repeat([]byte{0, 255, 7}, 32*1024)},
		}}},
		{"large_data", input{metadata: map[string]any{
			"text":   strings.Repeat("saved application data ", 32*1024),
			"values": []any{int64(-1), uint64(1<<63 + 7), []string{}, []string(nil)},
		}}},
	}
	for _, test := range cases {
		b.Run(test.name, func(b *testing.B) {
			codecs := NewCodecRegistry()
			recorder := &runStateRecorder{codecs: codecs, capture: true}
			loop := &turnTestLoop{next: func(execCtx *ExecutionContext) (*AgentLoopResult, error) {
				if test.input.metadata != nil {
					entry := &HistoryEntry{Messages: []*MessageContent{{Role: ChatMessageTypeAI,
						Parts: []ContentPart{TextPart("Saved provider or application content.")}}}}
					entry.SetMetadata("bench:saved", test.input.metadata)
					if err := execCtx.AppendRecord(entry, true); err != nil {
						return nil, err
					}
				}
				return acceptTurnText(execCtx, "The accepted answer remains available next turn.")
			}}
			runner := NewRunner[string](loop, &Config{Codecs: codecs, Recorder: recorder})
			execCtx, err := runner.NewExecutionContext(context.Background(), "codec benchmark")
			if err != nil {
				b.Fatal(err)
			}
			defer execCtx.Release()
			for turn := range 3 {
				parts := append([]ContentPart{TextPart(fmt.Sprintf("Task %d", turn))}, test.input.parts...)
				_, result, err := runner.NewTurn(execCtx, NewTask(parts...))
				if err != nil || result.Status != ExecutionFinished {
					b.Fatalf("turn: result %v, error %v", result, err)
				}
			}
			view := NewRunView(execCtx.RunId())
			for _, data := range recorder.batches {
				var batch SaveBatch
				if err := codecs.Unmarshal(data, &batch); err != nil {
					b.Fatal(err)
				}
				result, err := view.Apply(&batch, codecs)
				if err != nil || result.Status != ApplyApplied {
					b.Fatalf("replay: result %v, error %v", result, err)
				}
			}
			type savedCase struct {
				name  string
				value any
			}
			for _, saved := range []savedCase{
				{"state", recorder.states[len(recorder.states)-1].data}, {"view", view},
			} {
				b.Run(saved.name, func(b *testing.B) { benchmarkDocument(b, codecs, saved.value) })
			}
		})
	}
}

func benchmarkDocument(b *testing.B, codecs *CodecRegistry, value any) {
	type format struct {
		name   string
		encode func() ([]byte, error)
		decode func([]byte) error
	}
	formats := []format{
		{"envelope", func() ([]byte, error) {
			encoded, err := newValueEncoder(codecs).encode(reflect.ValueOf(value), 0)
			if err != nil {
				return nil, err
			}
			return json.Marshal(encoded)
		}, func(data []byte) error {
			var encoded EncodedValue
			if err := json.Unmarshal(data, &encoded); err != nil {
				return err
			}
			_, err := codecs.Decode(encoded)
			return err
		}},
		{"document", func() ([]byte, error) { return codecs.Marshal(value) },
			func(data []byte) error { var value any; return codecs.Unmarshal(data, &value) }},
	}
	for _, format := range formats {
		data, err := format.encode()
		if err != nil {
			b.Fatal(err)
		}
		b.Run(format.name+"/encode", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := format.encode(); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(len(data)), "saved-B")
		})
		b.Run(format.name+"/decode", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if err := format.decode(data); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(len(data)), "read-B")
		})
	}
}
