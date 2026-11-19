package gent

import (
	core "github.com/rickchristie/gent/internal/core"
	"time"
)

// TimeProvider supplies time and standard formats to application code. Use it when scheduling,
// deadlines, or business hours need a fixed test clock or a specific time zone. The caller composes
// prompt text; Gent does not inject this interface into an agent or expand a prompt template.
//
//	clock := gent.TimeProviderFunc(func() time.Time { return time.Now().In(userLocation) })
//	task := &gent.Task{Messages: []*gent.ModelMessage{
//	    gent.TextParts(gent.ChatMessageTypeSystem, "Today is " + clock.Today() + "."),
//	    gent.TextParts(gent.ChatMessageTypeHuman, "Check tomorrow's bookings."),
//	}}
//
// Use NewMockTimeProvider with a fixed time for repeatable application tests. Implement
// TimeProvider
// when the formats must also change; TimeProviderFunc uses the standard formats.
type TimeProvider = core.TimeProvider

// DefaultTimeProvider is the standard TimeProvider using the system clock.
type DefaultTimeProvider = core.DefaultTimeProvider

// NewDefaultTimeProvider creates a new DefaultTimeProvider.
func NewDefaultTimeProvider() *DefaultTimeProvider {
	return core.NewDefaultTimeProvider()
}

// MockTimeProvider is a TimeProvider that returns a fixed time.
// Useful for testing time-dependent functionality.
type MockTimeProvider = core.MockTimeProvider

// NewMockTimeProvider creates a MockTimeProvider with the given fixed time.
func NewMockTimeProvider(t time.Time) *MockTimeProvider {
	return core.NewMockTimeProvider(t)
}
