package multiturntest

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	v3RunID  = "run:b67731a0a4921f0ca147018385ff4c05"
	v3TurnID = "turn:60792fcb437439102b5f9c630e14bfa1"
)

// These bytes came from a v3 Runner and Recorder. A round trip through today's writer would
// allow the writer and reader to lose the same old field and still pass the test.
func readV3Fixtures(t *testing.T) fs.FS {
	t.Helper()
	data, err := os.ReadFile("testdata/storage/v3.zip")
	require.NoError(t, err)
	const digest = "c6fac3fec4f41ea2985dd795e7a2296e27c052da279f768a201038bc1baf1756"
	require.Equal(t, digest, fmt.Sprintf("%x", sha256.Sum256(data)))
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)
	return archive
}

func v3File(t *testing.T, fixtures fs.FS, name string) []byte {
	t.Helper()
	data, err := fs.ReadFile(fixtures, name)
	require.NoError(t, err)
	return data
}

func v3State(t *testing.T, codecs *gent.CodecRegistry, data []byte) *gent.RunState {
	t.Helper()
	loaded, err := codecs.UnmarshalRunState(data)
	require.NoError(t, err)
	require.Equal(t, gent.RunStateReady, loaded.Status, loaded.Detail)
	return loaded.State
}

// Each Apply receives its own batch because successful replay transfers ownership.
func applyV3Batch(t *testing.T, codecs *gent.CodecRegistry, data []byte,
	apply func(*gent.SaveBatch, *gent.CodecRegistry) (*gent.ApplyStateResult, error),
	want gent.ApplyStateStatus,
) {
	t.Helper()
	var batch gent.SaveBatch
	require.NoError(t, codecs.Unmarshal(data, &batch))
	digest, err := gent.BatchDigest(&batch, codecs)
	require.NoError(t, err)
	require.Equal(t, batch.BatchId, digest)
	result, err := apply(&batch, codecs)
	require.NoError(t, err)
	require.Equal(t, &gent.ApplyStateResult{Status: want}, result)
}

func v3Task() *gent.Task {
	return &gent.Task{
		Instructions: []*gent.ModelMessage{gent.TextParts(gent.ChatMessageTypeSystem,
			"Keep the audit record.")},
		Messages: []*gent.ModelMessage{
			{Role: gent.ChatMessageTypeAI, Parts: []gent.ContentPart{
				&gent.AssistantContent{Type: gent.AssistantContentFinalAnswer, MessageId: "import-1",
					Content: []gent.ContentPart{gent.TextPart("Imported receipt."),
						gent.PDFPart([]byte{0, 255, 7})}}},
				Continuation: &gent.ProviderContinuation{Provider: "fixture", Items: []json.RawMessage{
					json.RawMessage(` { "signature": "\u0061", "id": 18446744073709551615 } `),
				}}},
			gent.TextParts(gent.ChatMessageTypeHuman, "Save two records and report."),
		},
	}
}

func TestV3RunRecovery(t *testing.T) {
	type input struct {
		capture string
		start   int
		head    int
	}
	type expected struct {
		state    string
		sequence uint64
		visits   int
		recovery int
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	fixtures := readV3Fixtures(t)
	lines := bytes.Split(bytes.TrimSuffix(v3File(t, fixtures, "batches.jsonl"), []byte{'\n'}),
		[]byte{'\n'})
	require.Len(t, lines, 11)
	toolReady := expected{"tool-ready.json", 8, 1, 1}
	paused := expected{"paused.json", 17, 2, 0}
	finished := expected{"finished.json", 27, 3, 0}
	for _, test := range []testCase{
		{"unfinished external operation", input{"tool-ready.json", 3, 3}, toolReady},
		{"paused from batches", input{"", 0, 10}, paused},
		{"paused from capture with tail", input{"tool-ready.json", 3, 10}, paused},
		{"paused from latest capture", input{"paused.json", 10, 10}, paused},
		{"finished from batches", input{"", 0, 11}, finished},
		{"finished from capture with tail", input{"paused.json", 10, 11}, finished},
		{"finished from latest capture", input{"finished.json", 11, 11}, finished},
	} {
		t.Run(test.name, func(t *testing.T) {
			codecs := gent.NewCodecRegistry()
			store := &matrixStore{codecs: codecs, replay: matrixTail}
			// The external system still has the first write. Recovery must inspect it once
			// only if the saved state precedes its accepted recovery evidence.
			external := &matrixExternal{crashed: true, paused: true,
				calls: map[string]int{v3RunID + ":root:tool:1": 1}}
			runner := matrixRunner(t, store, external, true)
			state := gent.NewRunState(v3RunID)
			if test.input.capture != "" {
				state = v3State(t, codecs, v3File(t, fixtures, test.input.capture))
			}
			for _, line := range lines[test.input.start:test.input.head] {
				applyV3Batch(t, codecs, line, state.Apply, gent.ApplyApplied)
			}
			applyV3Batch(t, codecs, lines[test.input.head-1], state.Apply, gent.ApplyDuplicate)
			oldState := v3File(t, fixtures, test.expected.state)
			assert.Equal(t, v3State(t, codecs, oldState), state)
			assert.Equal(t, test.expected.sequence, state.LastAppliedEventSequenceNumber())
			restored, err := runner.Restore(context.Background(), state)
			require.NoError(t, err)
			require.Equal(t, gent.RestoreReady, restored.Status, restored.Detail)
			execCtx := restored.Context
			defer execCtx.Release()
			assert.Equal(t, v3RunID, execCtx.RunId())
			assert.Equal(t, v3TurnID, execCtx.TurnId())
			data := &matrixData{Name: "original", Visits: test.expected.visits, Resets: 1,
				Compacted: test.input.head >= 10, Tags: []string{"first", "second"},
				Counts: map[string]int{"kept": 7, "local": test.expected.visits, "zero": 0}}
			data.Nested.Enabled = true
			assert.Equal(t, data, execCtx.Data())
			task, err := execCtx.Task()
			require.NoError(t, err)
			assert.Equal(t, v3Task(), task)

			wantWork := matrixWork{Calls: map[string]int{v3RunID + ":root:tool:1": 1}}
			assert.Equal(t, wantWork, external.work())
			done := &gent.ExecutionResult{Status: gent.ExecutionFinished, TurnId: v3TurnID,
				TerminationReason: gent.TerminationSuccess, Output: []gent.ContentPart{gent.TextPart("Done")}}
			var value string
			var result *gent.ExecutionResult
			if test.input.head == 11 {
				value, result, err = runner.GetLastTurnResult(execCtx)
				require.NoError(t, err)
				assert.Equal(t, "Done", value)
				assert.Equal(t, done, result)
				assert.Equal(t, wantWork, external.work())
				value, result, err = runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Follow up.")))
				require.NoError(t, err)
				require.NotEqual(t, v3TurnID, result.TurnId)
				done.TurnId = result.TurnId
			} else {
				value, result, err = runner.Continue(execCtx)
				require.NoError(t, err)
			}
			assert.Equal(t, "Done", value)
			assert.Equal(t, done, result)
			wantWork.Models, wantWork.Recovery = 1, test.expected.recovery
			assert.Equal(t, wantWork, external.work())
			// A fresh writer appended this tail after the original v3 head. Both generations
			// of receipts must participate in normal replay through the new terminal save.
			require.NotEmpty(t, store.batches)
			replayed := v3State(t, codecs, oldState)
			for _, batch := range store.batches {
				applyV3Batch(t, codecs, batch, replayed.Apply, gent.ApplyApplied)
			}
			assert.Equal(t, v3State(t, codecs, store.states[len(store.states)-1]), replayed)
		})
	}
}

func TestV3RunViewAcceptsTail(t *testing.T) {
	fixtures := readV3Fixtures(t)
	lines := bytes.Split(bytes.TrimSuffix(v3File(t, fixtures, "batches.jsonl"), []byte{'\n'}),
		[]byte{'\n'})
	require.Len(t, lines, 11)
	codecs := gent.NewCodecRegistry()
	store := &matrixStore{codecs: codecs, replay: matrixTail}
	runner := matrixRunner(t, store, &matrixExternal{crashed: true, paused: true,
		calls: make(map[string]int)}, true)
	var saved gent.RunView
	require.NoError(t, codecs.Unmarshal(v3File(t, fixtures, "archive.json"), &saved))
	replayed := gent.NewRunView(v3RunID)
	for _, data := range lines[:10] {
		applyV3Batch(t, codecs, data, replayed.Apply, gent.ApplyApplied)
	}
	assert.Equal(t, replayed, &saved)
	require.Len(t, saved.Activity, 17)
	assert.Equal(t, gent.PhasePaused, saved.Checkpoint.Phase)
	var original, summaries []string
	var native []*gent.ProviderContinuation
	for _, entry := range saved.History.Entries {
		for _, message := range entry.Messages {
			if entry.Origin == gent.RecordUserInput {
				original = append(original, gent.ContentText(message.Parts))
			}
			if entry.Kind == gent.HistorySummary {
				summaries = append(summaries, gent.ContentText(message.Parts))
			}
			if message.Continuation != nil {
				native = append(native, message.Continuation)
			}
		}
	}
	assert.Equal(t, []string{"Save two records and report."}, original)
	assert.Equal(t, []string{"The saved tool evidence is available."}, summaries)
	assert.Equal(t, []*gent.ProviderContinuation{v3Task().Messages[0].Continuation}, native)
	applyV3Batch(t, codecs, lines[0], saved.Apply, gent.ApplyDuplicate)
	applyV3Batch(t, codecs, lines[10], saved.Apply, gent.ApplyApplied)
	applyV3Batch(t, codecs, lines[10], replayed.Apply, gent.ApplyApplied)
	assert.Equal(t, replayed, &saved)
	assert.Len(t, saved.Activity, 27)
	assert.Len(t, saved.Batches, 11)
	assert.Equal(t, gent.PhaseTerminal, saved.Checkpoint.Phase)

	// Follow the historical tail with actual batches from the current writer on the same run.
	state := v3State(t, codecs, v3File(t, fixtures, "finished.json"))
	restored, err := runner.Restore(context.Background(), state)
	require.NoError(t, err)
	require.Equal(t, gent.RestoreReady, restored.Status, restored.Detail)
	defer restored.Context.Release()
	value, result, err := runner.NewTurn(restored.Context, gent.NewTask(gent.TextPart("Follow up.")))
	require.NoError(t, err)
	require.Equal(t, gent.ExecutionFinished, result.Status)
	assert.Equal(t, "Done", value)
	require.NotEmpty(t, store.batches)
	for _, data := range store.batches {
		applyV3Batch(t, codecs, data, saved.Apply, gent.ApplyApplied)
		applyV3Batch(t, codecs, data, replayed.Apply, gent.ApplyApplied)
	}
	assert.Equal(t, replayed, &saved)
	assert.Equal(t, result, saved.Checkpoint.LastOutcome.Result)
	assert.Len(t, saved.Batches, 11+len(store.batches))
}
