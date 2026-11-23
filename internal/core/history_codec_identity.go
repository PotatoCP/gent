package gent

import (
	"reflect"
	"strings"
)

// These names describe values or types without a registry binding. Reject them before changing
// either map: a collision can make accepted state unreadable or change an unrelated container's
// type. Reserve the full derived-type prefixes, including names with incomplete syntax. Keep this
// rule aligned with decodeExpected and resolveTypeAtDepth when adding a wire tag or type form.
func reservedCodecName(id string) bool {
	switch id {
	case "bytes", "nil", "pointer", "slice", "array", "map", "time", "error", "encoded-value",
		"raw-json-bytes", "raw-json-nil", "any", "content", "error-interface":
		return true
	}
	return strings.HasPrefix(id, "*") || strings.HasPrefix(id, "[") || strings.HasPrefix(id, "map:")
}

// Framework wire names belong to the public API. Moving its implementation must not expose an
// internal package path or change the meaning of saved type identities.
func valueTypeId(typ reflect.Type) string {
	path := typ.PkgPath()
	if path == "github.com/rickchristie/gent/internal/core" {
		path = "github.com/rickchristie/gent"
	}
	return path + "/" + typ.Name()
}
