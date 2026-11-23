package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/rickchristie/gent"
)

type frameKind string

const (
	// frameReset supplies the accepted archive at a fixed head and any current live previews.
	frameReset frameKind = "reset"
	// frameEvent carries one ordered Gent event from a configured Subscriber.
	frameEvent frameKind = "event"
	// frameIdle tells the browser to reload accepted facts after a worker releases its context.
	frameIdle frameKind = "idle"
	// frameHeartbeat keeps an idle transport connection open without changing event state.
	frameHeartbeat frameKind = "heartbeat"
)

type displayItem struct {
	ID      string                    `json:"id"`
	Kind    itemKind                  `json:"kind"`
	Label   string                    `json:"label"`
	Text    string                    `json:"text"`
	Args    string                    `json:"args,omitempty"`
	Status  toolStatus                `json:"status,omitempty"`
	Type    gent.AssistantContentType `json:"type,omitempty"`
	Delta   bool                      `json:"delta,omitempty"`
	Sources []string                  `json:"sources,omitempty"`
}

type itemKind string

const (
	// itemUser contains saved user input, identified by its history origin.
	itemUser itemKind = "user"
	// itemAssistant contains readable model content or an accepted answer.
	itemAssistant itemKind = "assistant"
	// itemTool contains a tool's progress and safe result.
	itemTool itemKind = "tool"
	// itemActivity contains technical progress that belongs in the inspector.
	itemActivity itemKind = "activity"
)

type toolStatus string

const (
	// toolRunning means that the tool has started and has no returned result yet.
	toolRunning toolStatus = "running"
	// toolComplete means that execution or recovery returned an executed result.
	toolComplete toolStatus = "complete"
	// toolAttention means that the tool returned guidance, a block, or an unresolved result.
	toolAttention toolStatus = "attention"
)

type displayEvent struct {
	EventSequenceNumber uint64        `json:"eventSequenceNumber"`
	RunId               string        `json:"runId"`
	TurnId              string        `json:"turnId"`
	AttemptId           string        `json:"attemptId"`
	Name                string        `json:"name"`
	Items               []displayItem `json:"items"`
	Record              string        `json:"record"`
}

type browserFrame struct {
	Kind     frameKind               `json:"kind"`
	Delivery gent.SubscriberDelivery `json:"delivery,omitempty"`
	Event    *displayEvent           `json:"event,omitempty"`
	State    *browserState           `json:"state,omitempty"`
}

// Chat content uses concrete public payloads and safe tool replies. The separate local inspector
// receives the full codec representation, including native evidence. It is not model input.
func project(event *gent.RecordedEvent, codecs *gent.CodecRegistry) (*displayEvent, error) {
	encoded, err := codecs.Marshal(event)
	if err != nil {
		return nil, fmt.Errorf("inspect event %d: %w", event.EventSequenceNumber, err)
	}
	result := &displayEvent{EventSequenceNumber: event.EventSequenceNumber, RunId: event.RunId,
		TurnId: event.TurnId, AttemptId: event.AttemptId, Name: event.Type, Record: string(encoded)}
	if event.EventName != "" {
		result.Name = event.EventName
	}
	switch payload := event.Payload.(type) {
	case *gent.HistoryChange:
		for _, entry := range payload.AppendedEntries {
			if entry.Origin != gent.RecordUserInput {
				continue
			}
			for index, message := range entry.Messages {
				result.Items = append(result.Items, displayItem{
					ID: fmt.Sprintf("%s-%d", entry.Id, index), Kind: itemUser, Label: "You",
					Text: gent.ContentText(message.Parts)})
			}
		}
	case *gent.BeforeModelCallEvent:
		result.Items = []displayItem{{ID: payload.ModelCallId + "-progress", Kind: itemActivity,
			Label: "Model call",
			Text:  "Waiting for " + payload.Model + " · " + payload.Provider}}
	case *gent.StreamChunk:
		result.Items = projectParts(payload.Content, event.Type != "accepted_answer")
		for index := range result.Items {
			result.Items[index].Sources = payload.SourceMessageIds
		}
	case *gent.AfterModelCallEvent:
		result.Items = []displayItem{{ID: payload.ModelCallId + "-progress", Kind: itemActivity,
			Label: "Model call",
			Text:  "Response received"}}
		if payload.Error != nil {
			result.Items[0].Text = "Call interrupted; inspect server logs for diagnostics"
		}
		if payload.Response == nil {
			break
		}
		for _, choice := range payload.Response.Choices {
			result.Items = append(result.Items, projectParts(choice.Parts, false)...)
			if choice.Continuation != nil {
				result.Items = append(result.Items, displayItem{ID: payload.ModelCallId + "-native",
					Kind: itemActivity, Label: "Provider context",
					Text: "Native continuation is stored for replay. " +
						"This is not readable reasoning."})
			}
		}
	case *gent.BeforeToolCallEvent:
		result.Items = []displayItem{{ID: payload.ToolCallId, Kind: itemTool,
			Label: payload.ToolName,
			Args:  jsonText(payload.Args, codecs), Status: toolRunning}}
	case *gent.AfterToolCallEvent:
		text := "Result saved in the archive"
		status := toolAttention
		if record, ok := payload.Output.(*gent.ToolCallRecord); ok {
			var err error
			text, err = toolResultText(record.Response().Parts())
			if err != nil {
				return nil, err
			}
			execution, ok := record.Response().ExecutionStatus()
			if ok && execution == gent.ToolExecuted {
				status = toolComplete
			}
		}
		result.Items = []displayItem{{ID: payload.ToolCallId, Kind: itemTool,
			Label: payload.ToolName,
			Text:  text, Status: status}}
	}
	return result, nil
}

// Domain tools return structured DataContent. Show it as JSON without changing saved content.
func toolResultText(parts []gent.ContentPart) (string, error) {
	var blocks []string
	for _, part := range parts {
		var value any
		switch part := part.(type) {
		case gent.DataContent:
			value = part.Value()
		case *gent.DataContent:
			value = part.Value()
		default:
			if text := gent.ContentText([]gent.ContentPart{part}); text != "" {
				blocks = append(blocks, text)
			}
			continue
		}
		data, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return "", fmt.Errorf("display tool result: %w", err)
		}
		blocks = append(blocks, "```json\n"+string(data)+"\n```")
	}
	return strings.Join(blocks, "\n\n"), nil
}

func jsonText(value any, codecs *gent.CodecRegistry) string {
	if encoded, ok := value.(*gent.EncodedValue); ok {
		decoded, err := codecs.Decode(*encoded)
		if err != nil {
			log.Printf("display argument decode: %v", err)
			return "Arguments unavailable for display"
		}
		value = decoded
	}
	data, err := json.Marshal(value)
	if err != nil {
		log.Printf("display arguments: %v", err)
		return "Arguments unavailable for display"
	}
	return string(data)
}

func projectParts(parts []gent.ContentPart, delta bool) []displayItem {
	var items []displayItem
	for _, part := range parts {
		item, ok := part.(*gent.AssistantContent)
		if !ok {
			continue
		}
		label := "Original model response"
		switch item.Type {
		case gent.AssistantContentReasoning:
			label = "Reasoning exposed by provider"
		case gent.AssistantContentCommentary:
			label = "Assistant progress"
		case gent.AssistantContentFinalAnswer:
			label = "Accepted answer · processed output"
		}
		items = append(items, displayItem{ID: item.MessageId, Kind: itemAssistant, Label: label,
			Text: gent.ContentText(item.Content), Type: item.Type, Delta: delta})
	}
	return items
}

type eventHub struct {
	mu    sync.Mutex
	rooms map[string]*eventRoom
}

type eventRoom struct {
	clients  map[chan []byte]bool
	attempt  string
	previews []*displayEvent
	bytes    int
	gap      bool
}

func newEventHub() *eventHub { return &eventHub{rooms: map[string]*eventRoom{}} }

func (hub *eventHub) clear() {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	for _, room := range hub.rooms {
		for queue := range room.clients {
			delete(room.clients, queue)
			discardEvents(queue)
			close(queue)
		}
	}
	clear(hub.rooms)
}

// Reset drops pending display bytes before closing the channel. Otherwise an open browser
// could drain old frames before it detects the reset. The hub lock excludes concurrent sends.
func discardEvents(queue chan []byte) {
	for {
		select {
		case <-queue:
		default:
			return
		}
	}
}

func (hub *eventHub) room(runId string) *eventRoom {
	room := hub.rooms[runId]
	if room == nil {
		room = &eventRoom{clients: map[chan []byte]bool{}}
		hub.rooms[runId] = room
	}
	return room
}

func (hub *eventHub) subscribe(runId string) (chan []byte, func()) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	room, queue := hub.room(runId), make(chan []byte, 64)
	room.clients[queue] = true
	return queue, func() {
		hub.mu.Lock()
		defer hub.mu.Unlock()
		if room.clients[queue] {
			delete(room.clients, queue)
			close(queue)
		}
	}
}

func (hub *eventHub) publish(runId string, frame *browserFrame) error {
	data, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	room := hub.room(runId)
	if frame.Kind == frameIdle {
		room.previews, room.bytes, room.attempt, room.gap = nil, 0, "", false
	}
	if frame.Event != nil && frame.Delivery == gent.SubscriberLive {
		if room.attempt != frame.Event.AttemptId {
			room.previews, room.bytes, room.gap = nil, 0, false
			room.attempt = frame.Event.AttemptId
		}
		if room.bytes+len(data) > 1<<20 || len(room.previews) >= 256 {
			room.previews, room.bytes, room.gap = nil, 0, true
		}
		if len(data) <= 1<<20 {
			room.previews = append(room.previews, frame.Event)
			room.bytes += len(data)
		}
	}
	for queue := range room.clients {
		// A browser gets a bounded queue. Disconnect a slow reader and let it reload the archive;
		// it must never block Gent's subscriber or change the accepted result.
		if len(data) <= 256<<10 {
			select {
			case queue <- data:
				continue
			default:
			}
		}
		delete(room.clients, queue)
		close(queue)
	}
	return nil
}

func (hub *eventHub) previews(runId string, head uint64) ([]*displayEvent, bool) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	room := hub.room(runId)
	var events []*displayEvent
	for _, event := range room.previews {
		if event.EventSequenceNumber > head {
			events = append(events, event)
		}
	}
	return events, room.gap
}

type eventSubscriber struct {
	hub      *eventHub
	delivery gent.SubscriberDelivery
	codecs   *gent.CodecRegistry
}

func (subscriber *eventSubscriber) OnEvent(ctx context.Context, event *gent.RecordedEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	eventView, err := project(event, subscriber.codecs)
	if err != nil {
		return err
	}
	return subscriber.hub.publish(event.RunId, &browserFrame{Kind: frameEvent,
		Delivery: subscriber.delivery, Event: eventView})
}

func (subscriber *eventSubscriber) OnStop(_ context.Context, stop *gent.SubscriberStop) {
	if stop.Error != nil || stop.Reason != gent.SubscriberClosed {
		log.Printf("subscriber %s run %s stopped: %s: %v", subscriber.delivery,
			stop.RunId, stop.Reason, stop.Error)
	}
}
