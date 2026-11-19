package gent_test

import (
	"fmt"
	"github.com/rickchristie/gent"
)

// Each recorder owns its archive. Decode the borrowed batch before transferring ownership to
// Apply. Do not copy the growing archive at every save; no caller keeps an earlier mutable view.
func applyTestBatch(view *gent.RunView, batch *gent.SaveBatch,
	registries ...*gent.CodecRegistry,
) error {
	codecs := gent.NewCodecRegistry()
	if len(registries) != 0 {
		codecs = registries[0]
	}
	saved, err := codecs.Marshal(batch)
	if err != nil {
		return err
	}
	var owned gent.SaveBatch
	if err := codecs.Unmarshal(saved, &owned); err != nil {
		return err
	}
	result, err := view.Apply(&owned, codecs)
	if err != nil {
		return err
	}
	if result.Status != gent.ApplyApplied && result.Status != gent.ApplyDuplicate {
		return fmt.Errorf("test archive replay: %s: %s", result.Status, result.Detail)
	}
	return nil
}
