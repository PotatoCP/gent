package gent

import "errors"

// A cancellation match in one branch does not explain other failures in a joined error. Only
// suppress an error when every leaf is the active context error or its cancellation cause.
func onlyCancellation(err, contextErr, cause error) bool {
	if err == nil || contextErr == nil {
		return false
	}
	switch wrapped := err.(type) {
	case interface{ Unwrap() []error }:
		children := wrapped.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !onlyCancellation(child, contextErr, cause) {
				return false
			}
		}
		return true
	case interface{ Unwrap() error }:
		if child := wrapped.Unwrap(); child != nil {
			return onlyCancellation(child, contextErr, cause)
		}
	}
	return errors.Is(err, contextErr) || errors.Is(err, cause)
}
