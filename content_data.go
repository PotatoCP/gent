package gent

import (
	core "github.com/rickchristie/gent/internal/core"
)

// DataContent is captured domain data, separate from authored text. Values use portable maps,
// slices, scalars, and number types. It does not retain a named Go type or its methods.
// Use DataPart to construct it. Value returns a read-only view owned by the part.
type DataContent = core.DataContent

// DataPart captures a Go value with its JSON field names, nulls, and numeric meaning. Gent owns
// serialization; the caller needs no output-type registration. Capture rejects cycles, functions,
// channels, non-finite numbers, and invalid custom JSON. It returns no part on failure.
// The capture is a stable portable value, not restoration of the original Go type. TextPart lets
// the caller choose exact text instead. Later changes to the original value do not change it.
// A custom JSON encoder owns its representation. Its numeric tokens determine captured numeric
// meaning; Gent does not infer that meaning from the encoder's private Go fields or elements.
func DataPart(value any) (ContentPart, error) {
	return core.DataPart(value)
}
