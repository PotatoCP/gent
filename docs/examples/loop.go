package examples

import (
	"fmt"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/output"
)

// docs:start loop-custom
type LocalLoop struct{}

func (*LocalLoop) Output() gent.Output[string] { return output.NewTextOutput() }

func (loop *LocalLoop) Next(execCtx *gent.ExecutionContext) (*gent.AgentLoopResult, error) {
	checked, err := loop.Output().Process(execCtx, []gent.ContentPart{gent.TextPart("Ready.")})
	if err != nil {
		return nil, err
	}
	// AcceptOutput prepares the value. The Runner saves it before it returns the answer.
	return gent.AcceptOutput(execCtx, checked, nil)
}

// docs:end loop-custom

// docs:start loop-data
type Progress struct {
	Files     int
	TurnFiles int
}

type LoopData struct {
	Progress
	codecs *gent.CodecRegistry
}

func (*LoopData) CheckpointType() string { return "example:progress" }
func (*LoopData) CheckpointVersion() int { return 1 }

func (data *LoopData) ResetTurn() {
	// Local assignment only. Keep the cumulative Files count.
	data.TurnFiles = 0
}

// docs:end loop-data

// docs:start loop-data-save
func (data *LoopData) SaveCheckpointState() (*gent.EncodedValue, error) {
	value, err := data.codecs.Encode(data.Progress)
	return &value, err
}

func (data *LoopData) RestoreCheckpointState(saved *gent.EncodedValue) error {
	value, err := data.codecs.Decode(*saved)
	if err != nil {
		return err
	}
	progress, ok := value.(Progress)
	if !ok {
		return fmt.Errorf("progress has unexpected saved type %T", value)
	}
	data.Progress = progress
	return nil
}

// docs:end loop-data-save

func loopDataConfig() (*gent.Config, error) {
	// docs:start loop-data-factory
	codecs := gent.NewCodecRegistry()
	if err := codecs.RegisterType("example:progress-value", 1, Progress{}); err != nil {
		return nil, err
	}
	config := &gent.Config{
		Codecs:      codecs,
		NewLoopData: func() gent.LoopData { return &LoopData{codecs: codecs} },
	}
	// The factory creates empty data. Restore fills it; NewTurn calls ResetTurn.
	// docs:end loop-data-factory
	return config, nil
}

func recordFile(execCtx *gent.ExecutionContext) {
	// docs:start loop-data-use
	data := execCtx.Data().(*LoopData)
	data.Files++
	data.TurnFiles++
	// Change local progress inside Next. The next approved save captures these fields together.
	// docs:end loop-data-use
}

func appendHistory(execCtx *gent.ExecutionContext) error {
	// docs:start loop-history
	entry, err := execCtx.NewHistoryEntry(&gent.HistoryEntry{
		Messages: []*gent.MessageContent{{
			Role:  gent.ChatMessageTypeHuman,
			Parts: []gent.ContentPart{gent.TextPart("The local check passed.")},
		}},
	})
	if err != nil {
		return err
	}
	draft := execCtx.ContextDraft()
	return execCtx.UpdateHistory(gent.HistoryUpdate{
		ExpectedDraftRevision: draft.Revision,
		AppendedEntries:       []*gent.HistoryEntry{entry},
		Active:                append(draft.Active, entry.Id),
	})
	// docs:end loop-history
}
