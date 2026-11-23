package gent

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMapArchiveRejectsChangedProjection(t *testing.T) {
	type input struct{ change func(*RunView) }
	type expected struct{ errorText string }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	retained := expected{"gent: archive retained projection differs from its replay state"}
	disagree := expected{"gent: archive and replay state disagree"}
	cases := []testCase{
		{"history value", input{func(view *RunView) {
			value, _ := mapHistoryEntry(t, view.History).GetMetadata("app:map")
			for key := range value.(map[*int]string) {
				value.(map[*int]string)[key] = "altered value"
				break
			}
		}}, retained},
		{"history concrete type", input{func(view *RunView) {
			mapHistoryEntry(t, view.History).SetMetadata("app:map", map[string]string{})
		}}, retained},
		{"runtime", input{func(view *RunView) { view.Runtime.Allocation++ }}, disagree},
		{"checkpoint", input{func(view *RunView) {
			view.Checkpoint.Contexts[0].Name = "altered name"
		}}, disagree},
		{"retained evidence", input{func(view *RunView) {
			for _, event := range view.Activity {
				if before, ok := event.Payload.(*BeforeModelCallEvent); ok {
					before.Request.Options.Metadata["app:map"] = "altered evidence"
					return
				}
			}
			t.Fatal("managed model request is missing")
		}}, retained},
	}
	for _, compression := range []CodecCompression{CodecCompressionNone, CodecCompressionSnappy} {
		t.Run(string(compression), func(t *testing.T) {
			codecs := NewCodecRegistry(WithCodecCompression(compression))
			recorder, runId, _ := recordSavedMap(t, codecs,
				savedPointerMap(savedMapEntries(false, false)))
			view := NewRunView(runId)
			applyMapArchive(t, view, codecs, recorder.batches)
			original := marshalMapArchive(t, codecs, view)
			for _, test := range cases {
				t.Run(test.name, func(t *testing.T) {
					// Corrupt only a decoded public projection. The private replay value must
					// remain separate and detect each real difference after the next load.
					changed := loadMapArchive(t, codecs, original)
					test.input.change(changed)
					data := marshalMapArchive(t, codecs, changed)
					target := NewRunView("keep-target")
					before := marshalMapArchive(t, codecs, target)
					err := codecs.Unmarshal(data, target)
					if assert.Error(t, err) {
						assert.Equal(t, test.expected, expected{err.Error()})
					}
					assert.Equal(t, before, marshalMapArchive(t, codecs, target))
				})
			}
		})
	}
}

func TestMapArchiveComparisonUsesStoredByteLimit(t *testing.T) {
	codecs := NewCodecRegistry()
	recorder, runId, _ := recordSavedMap(t, codecs, savedPointerMap(savedMapEntries(true, false)))
	view := NewRunView(runId)
	applyMapArchive(t, view, codecs, recorder.batches)
	data := marshalMapArchive(t, codecs, view)
	component, err := codecs.Encode(view)
	if !assert.NoError(t, err) {
		return
	}
	expanded, err := json.Marshal(component)
	assert.NoError(t, err)
	assert.Greater(t, len(expanded), len(data))
	bounded := NewCodecRegistry(WithCodecLimits(CodecLimits{MaxEncodedBytes: len(data)}))
	// Logical equality must not call the public component encoder: its expanded component
	// buffer has a different size from this valid compressed storage document.
	var restored RunView
	if !assert.NoError(t, bounded.Unmarshal(data, &restored)) {
		return
	}
	assert.Equal(t, data, marshalMapArchive(t, bounded, &restored))
}

func TestMapArchiveComparisonPreservesCodecFailures(t *testing.T) {
	type input struct{ compression CodecCompression }
	type expected struct{ message, stateMessage string }
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	failure := errors.New("comparison encoder failed")
	path := strings.Join([]string{"gent.HistorySnapshot.Entries", "gent.HistoryEntry.Metadata"}, ": ")
	message := "gent: compare archive retained projection: " + path +
		": encode test/run-state-value: comparison encoder failed"
	statePath := strings.Join([]string{"runStateData.History", "HistorySnapshot.Entries",
		"HistoryEntry.Metadata"}, ": github.com/rickchristie/gent/")
	want := expected{message, "gent: decode RunStateSnapshot: github.com/rickchristie/gent/" +
		statePath + ": " + message}
	for _, test := range []testCase{
		{"plain", input{CodecCompressionNone}, want},
		{"compressed", input{CodecCompressionSnappy}, want},
	} {
		t.Run(test.name, func(t *testing.T) {
			good := NewCodecRegistry(WithCodecCompression(test.input.compression))
			assert.NoError(t, good.Register(runStateCustomValue{}, runStateEncodeCodec{}))
			value := []any{savedPointerMap(savedMapEntries(false, false)),
				runStateCustomValue{Label: "saved custom value"}}
			recorder, runId, _ := recordSavedMap(t, good, value)
			view := NewRunView(runId)
			applyMapArchive(t, view, good, recorder.batches)
			data := marshalMapArchive(t, good, view)
			// A supported RunView saved as application metadata also exercises the typed loader.
			// Its nested decoder must retain an unexpected comparison-codec failure as an error.
			outerId, batches := recordedStateFixture(t, 1, good, view)
			outer := NewRunState(outerId)
			for _, batch := range batches {
				applyRecordedState(t, outer, good, batch)
			}
			snapshot := encodeRunState(t, good, outer)
			codecs := NewCodecRegistry()
			assert.NoError(t, codecs.Register(runStateCustomValue{},
				runStateEncodeCodec{failure: failure}))
			target := NewRunView("keep-target")
			before := marshalMapArchive(t, good, target)
			err := codecs.Unmarshal(data, target)
			assert.EqualError(t, err, test.expected.message)
			assert.ErrorIs(t, err, failure)
			assert.Equal(t, before, marshalMapArchive(t, good, target))
			loaded, err := codecs.UnmarshalRunState(snapshot)
			assert.Nil(t, loaded)
			assert.ErrorIs(t, err, failure)
			assert.EqualError(t, err, test.expected.stateMessage)
			// A corrected registry must still load both original byte sequences.
			loadMapArchive(t, good, data)
			loaded, err = good.UnmarshalRunState(snapshot)
			if assert.NoError(t, err) {
				assert.Equal(t, &RunStateLoadResult{Status: RunStateReady, State: loaded.State}, loaded)
			}
		})
	}
}
