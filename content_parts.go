package gent

import (
	core "github.com/rickchristie/gent/internal/core"
)

// ContentText returns a text preview. It does not convert or validate media. Output processors
// must inspect all parts instead of using this helper to discard unsupported content.
func ContentText(parts []ContentPart) string {
	return core.ContentText(parts)
}

// CloneContentParts owns each part, including its nested bytes and native tool data. ContentPart
// is a closed set, so stream snapshots do not need reflection or serialization for every delta.
func CloneContentParts(parts []ContentPart) []ContentPart {
	return core.CloneContentParts(parts)
}
