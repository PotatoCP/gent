package trace

import (
	"context"

	"github.com/rickchristie/gent"
)

// OnEvent projects a borrowed event through the same path as saved archive replay. The configured
// redactor receives an independent view so display changes cannot mutate accepted execution data.
func (s *Sequencer) OnEvent(ctx context.Context, event *gent.RecordedEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.observeEvents([]*gent.RecordedEvent{event}, s.cfg.Codecs)
}

// OnStop retains delivery diagnostics separately from run results. A pause or completed turn can
// be followed by another attempt, so this callback does not close the conversation's display view.
// The application can read LastDeliveryStop to report a missed update and reload accepted history.
func (s *Sequencer) OnStop(_ context.Context, result *gent.SubscriberStop) {
	s.mu.Lock()
	s.lastDeliveryStop = result
	s.mu.Unlock()
}

// LastDeliveryStop returns the last attempt's delivery result, or nil before delivery has stopped.
// This is a borrowed read-only runtime value, not saved execution evidence. An unexpected callback
// error is retained in Error. A new attempt can replace the value without changing the prior one.
func (s *Sequencer) LastDeliveryStop() *gent.SubscriberStop {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastDeliveryStop
}

var _ gent.Subscriber = (*Sequencer)(nil)
