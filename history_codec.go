package gent

import (
	core "github.com/rickchristie/gent/internal/core"
)

// EncodedValue preserves concrete types across process boundaries. Ordinary JSON decoding of
// interface fields loses content-part implementations, numeric types, and typed tool metadata.
type EncodedValue = core.EncodedValue

// ValueCodec supplies a versioned representation for an application-owned value. Decode must
// return an owned value and enforce its own allocation limits. Gent supplies a separate input
// buffer for each Decode call; do not return shared mutable results across calls.
type ValueCodec = core.ValueCodec

// CodecRegistry binds wire names to concrete types. Register custom types before starting a run;
// decoding never loads code or guesses a Go type from an untrusted checkpoint.
type CodecRegistry = core.CodecRegistry

func NewCodecRegistry(options ...CodecOption) *CodecRegistry {
	return core.NewCodecRegistry(options...)
}
