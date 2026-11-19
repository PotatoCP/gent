package trace

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/internal/buffer"
)

// Sequencer projects frozen events into display snapshots and bounded subscriptions.
// Register it as a Config Subscriber. The registration selects Live or Accepted delivery.
type Sequencer struct {
	observeMu sync.Mutex
	closeMu   sync.Mutex
	mu        sync.RWMutex

	lastDeliveryStop *gent.SubscriberStop
	runId            string
	cfg              config

	closed                  bool
	nextEventSequenceNumber uint64

	snapshot Snapshot

	recentEvents          []*Event
	recentLifecycleEvents []*Event
	recentChunkEvents     []*Event

	contextById    map[string]*Context
	modelById      map[string]*ModelCall
	toolById       map[string]*ToolCall
	iterationByKey map[string]*Iteration

	liveSubscribers    map[uint64]*liveSubscriber
	nextLiveSubscriber uint64
}

// NewSequencer creates one conversation view. Empty runId binds the first observed event. This
// lets Config register the Subscriber before Runner allocates the conversation identity. Use a
// separate Sequencer per conversation; a bound view rejects events from another run.
func NewSequencer(runId string, cfg Config) (*Sequencer, error) {
	normalized := normalizeConfig(cfg)
	now := time.Now()
	seq := &Sequencer{
		runId: runId,
		cfg:   normalized,
		snapshot: Snapshot{
			SchemaVersion: SchemaVersion,
			RunId:         runId,
			Status:        RunStatusPending,
			StartedTs:     now,
			LastUpdatedTs: now,
		},
		contextById:     make(map[string]*Context),
		modelById:       make(map[string]*ModelCall),
		toolById:        make(map[string]*ToolCall),
		iterationByKey:  make(map[string]*Iteration),
		liveSubscribers: make(map[uint64]*liveSubscriber),
	}
	return seq, nil
}

func (s *Sequencer) record(event *Event) *Event {
	if event == nil {
		return nil
	}
	if event.Ts.IsZero() {
		event.Ts = time.Now()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}

	if event.EventSequenceNumber == 0 || event.EventSequenceNumber <= s.nextEventSequenceNumber {
		return nil
	}
	s.nextEventSequenceNumber = event.EventSequenceNumber
	event.RunId = s.runId
	s.fillErrorMetadata(event)
	s.applyEvent(event)
	event.Payload = jsonSafeValue(event.Payload)
	s.snapshot.LastEventSequenceNumber = event.EventSequenceNumber
	s.snapshot.LastUpdatedTs = event.Ts

	stored := cloneEvent(event)
	s.appendRecent(stored)
	s.sendLive(stored)
	return cloneEvent(stored)
}

// Snapshot returns a copy safe for concurrent readers.
func (s *Sequencer) Snapshot() *Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneSnapshot(&s.snapshot)
}

// EventsAfter returns replay events after lastEventSequenceNumber from the contiguous recent
// buffer.
func (s *Sequencer) EventsAfter(lastEventSequenceNumber uint64) ([]*Event, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if lastEventSequenceNumber == s.snapshot.LastEventSequenceNumber {
		return []*Event{}, nil
	}
	if len(s.recentEvents) == 0 {
		return nil, ErrReplayUnavailable
	}
	first := s.recentEvents[0].EventSequenceNumber
	if lastEventSequenceNumber+1 < first {
		return nil, ErrReplayUnavailable
	}
	result := make([]*Event, 0)
	for _, event := range s.recentEvents {
		if event.EventSequenceNumber > lastEventSequenceNumber {
			result = append(result, cloneEvent(event))
		}
	}
	if len(result) == 0 && lastEventSequenceNumber < s.snapshot.LastEventSequenceNumber {
		return nil, ErrReplayUnavailable
	}
	return result, nil
}

// Close waits for the current projection and closes live subscriptions.
func (s *Sequencer) Close() {
	s.closeMu.Lock()
	defer s.closeMu.Unlock()

	s.observeMu.Lock()
	defer s.observeMu.Unlock()

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	subs := s.liveSubscribers
	s.liveSubscribers = make(map[uint64]*liveSubscriber)
	s.mu.Unlock()

	for _, sub := range subs {
		sub.close()
	}
}

func (s *Sequencer) fillErrorMetadata(event *Event) {
	if event.Error == nil {
		return
	}
	if event.Error.EventSequenceNumber == 0 {
		event.Error.EventSequenceNumber = event.EventSequenceNumber
	}
	if event.Error.Ts.IsZero() {
		event.Error.Ts = event.Ts
	}
	if event.Error.EventName == "" {
		event.Error.EventName = event.EventName
	}
	if event.Error.Source == "" {
		event.Error.Source = event.Source
	}
	if event.Error.Iteration == 0 {
		event.Error.Iteration = event.Iteration
	}
	if event.Error.ContextId == "" {
		event.Error.ContextId = event.ContextId
	}
	if event.Error.ModelCallId == "" {
		event.Error.ModelCallId = event.ModelCallId
	}
	if event.Error.ToolCallId == "" {
		event.Error.ToolCallId = event.ToolCallId
	}
}

func (s *Sequencer) appendRecent(event *Event) {
	if s.cfg.maxRecentEvents >= 0 {
		s.recentEvents = appendCapped(s.recentEvents, cloneEvent(event), s.cfg.maxRecentEvents)
		s.snapshot.RecentEvents = s.recentEvents
	}
	if event.Type == EventTypeModelStreamChunk {
		if s.cfg.maxRecentChunkEvents >= 0 {
			s.recentChunkEvents = appendCapped(
				s.recentChunkEvents, cloneEvent(event), s.cfg.maxRecentChunkEvents,
			)
			s.snapshot.RecentChunkEvents = s.recentChunkEvents
		}
		return
	}
	if s.cfg.maxRecentLifecycleEvents >= 0 {
		s.recentLifecycleEvents = appendCapped(
			s.recentLifecycleEvents, cloneEvent(event), s.cfg.maxRecentLifecycleEvents,
		)
		s.snapshot.RecentLifecycleEvents = s.recentLifecycleEvents
	}
}

func appendCapped(events []*Event, event *Event, max int) []*Event {
	if max < 0 {
		return events
	}
	events = append(events, event)
	if len(events) <= max {
		return events
	}
	trim := len(events) - max
	copy(events, events[trim:])
	return events[:max]
}

func (s *Sequencer) sendLive(event *Event) {
	for id, sub := range s.liveSubscribers {
		if sub.send(cloneEvent(event)) {
			continue
		}
		delete(s.liveSubscribers, id)
	}
}

type liveSubscriber struct {
	id       uint64
	delivery *buffer.Delivery[*Event]

	bounded    chan *Event
	policy     OverflowPolicy
	onOverflow func(OverflowInfo)
	overflow   chan OverflowInfo

	closeOnce sync.Once
}

func (s *liveSubscriber) receive() <-chan *Event {
	if s.delivery != nil {
		return s.delivery.Receive()
	}
	return s.bounded
}

func (s *liveSubscriber) send(event *Event) bool {
	if s.delivery != nil {
		data, err := json.Marshal(event)
		if err != nil {
			s.delivery.Fail(err)
			return false
		}
		return s.delivery.Send(event, len(data))
	}
	select {
	case s.bounded <- event:
		return true
	default:
	}

	switch s.policy {
	case OverflowDropOldest:
		var dropped *Event
		select {
		case dropped = <-s.bounded:
		default:
		}
		select {
		case s.bounded <- event:
			s.notifyOverflow(OverflowInfo{
				Policy:                     s.policy,
				DroppedEventSequenceNumber: eventSequenceNumber(dropped),
				NewEventSequenceNumber:     event.EventSequenceNumber,
			})
			return true
		default:
			s.notifyOverflow(OverflowInfo{
				Policy:                     OverflowDropNewest,
				DroppedEventSequenceNumber: event.EventSequenceNumber,
				NewEventSequenceNumber:     event.EventSequenceNumber,
			})
			return true
		}
	case OverflowDropNewest:
		s.notifyOverflow(OverflowInfo{
			Policy:                     s.policy,
			DroppedEventSequenceNumber: event.EventSequenceNumber,
			NewEventSequenceNumber:     event.EventSequenceNumber,
		})
		return true
	default:
		s.notifyOverflow(OverflowInfo{
			Policy:                 OverflowCloseSubscriber,
			NewEventSequenceNumber: event.EventSequenceNumber,
			SubscriberClosed:       true,
		})
		s.close()
		return false
	}
}

func (s *liveSubscriber) notifyOverflow(info OverflowInfo) {
	if s.onOverflow == nil {
		return
	}
	// Coalesce notifications for a slow callback. One callback worker per subscription keeps
	// repeated drops from creating an unbounded number of goroutines.
	select {
	case s.overflow <- info:
	default:
	}
}

func (s *liveSubscriber) close() {
	s.closeOnce.Do(func() {
		if s.overflow != nil {
			close(s.overflow)
		}
		if s.delivery != nil {
			s.delivery.Close()
			return
		}
		close(s.bounded)
	})
}

func (s *liveSubscriber) discard() {
	if s.delivery != nil {
		s.delivery.Discard()
		<-s.delivery.Done()
		return
	}
	s.close()
	for range s.bounded {
	}
}

func eventSequenceNumber(event *Event) uint64 {
	if event == nil {
		return 0
	}
	return event.EventSequenceNumber
}
