package examples

import (
	"encoding/json"

	"github.com/rickchristie/gent"
)

// docs:start compaction-trigger
type LengthTrigger struct {
	Maximum float64
}

func (trigger *LengthTrigger) ShouldCompact(execCtx *gent.ExecutionContext) (bool, error) {
	return execCtx.Stats().GetGauge(gent.SGScratchpadLength) >= trigger.Maximum, nil
}

func (*LengthTrigger) NotifyCompacted(execCtx *gent.ExecutionContext) error {
	// This gauge trigger has no private counter baseline to save.
	return nil
}

// docs:end compaction-trigger

// docs:start codec-custom
type ProgressCodec struct{}

func (ProgressCodec) Type() string { return "example:progress-codec" }
func (ProgressCodec) Version() int { return 1 }

func (ProgressCodec) Encode(value any) (json.RawMessage, error) {
	return json.Marshal(value.(Progress))
}

func (ProgressCodec) Decode(data json.RawMessage) (any, error) {
	var value Progress
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, err
	}
	return value, nil
}

// docs:end codec-custom

func registerCodec() (*gent.CodecRegistry, error) {
	// docs:start codec-custom-register
	codecs := gent.NewCodecRegistry()
	if err := codecs.Register(Progress{}, ProgressCodec{}); err != nil {
		return nil, err
	}
	// Keep the codec's type ID and meaning stable while saved runs use it.
	// docs:end codec-custom-register
	return codecs, nil
}
