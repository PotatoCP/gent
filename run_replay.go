package gent

import (
	core "github.com/rickchristie/gent/internal/core"
)

// ValidateBundle reconstructs history from the complete activity sequence. A bundle cannot claim
// a cursor for missing events or silently substitute a newer selection for an older checkpoint.
func ValidateBundle(bundle RunView, registries ...*CodecRegistry) error {
	return core.ValidateBundle(bundle, registries...)
}
