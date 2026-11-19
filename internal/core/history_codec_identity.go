package gent

import "reflect"

// Framework wire names belong to the public API. Moving its implementation must not expose an
// internal package path or change the meaning of saved type identities.
func valueTypeId(typ reflect.Type) string {
	path := typ.PkgPath()
	if path == "github.com/rickchristie/gent/internal/core" {
		path = "github.com/rickchristie/gent"
	}
	return path + "/" + typ.Name()
}
