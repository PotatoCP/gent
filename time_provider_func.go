package gent

import (
	core "github.com/rickchristie/gent/internal/core"
)

// TimeProviderFunc uses a clock function with the standard prompt time formats.
// The function is required. Use TimeProvider when custom formats are also needed.
type TimeProviderFunc = core.TimeProviderFunc
