package rex

import (
	"context"
	"encoding/json"
	"time"
)

// ViewOnly means discovery may change focus or geometry, but no terminal data
// notification was received. Unknown/data events always require revalidation.
type Event struct {
	Received time.Time
	ViewOnly bool
	Count    int
	Kinds    uint16
}

func (e Event) Merge(other Event) Event {
	if other.Received.Before(e.Received) {
		e.Received = other.Received
	}
	e.Kinds |= other.Kinds
	e.ViewOnly = e.ViewOnly && other.ViewOnly
	e.Count = max(1, e.Count) + max(1, other.Count)
	return e
}

// Merge instead of dropping notifications when the consumer is taking a snapshot.
// In particular, a queued view event must never hide a later data change.
func enqueueEvent(ctx context.Context, notify chan Event, event Event) {
	for {
		select {
		case <-ctx.Done():
			return
		case notify <- event:
			return
		default:
		}
		select {
		case notify <- event:
			return
		case previous := <-notify:
			event = event.Merge(previous)
		case <-ctx.Done():
			return
		}
	}
}

// Only view notifications are safe to reuse without data revalidation.
// Block events are deliberately conservative, including unfamiliar subtypes.
func parseEvent(data []byte) (Event, bool) {
	var raw struct {
		Type    string `json:"type"`
		Payload struct {
			Event struct {
				Type string `json:"type"`
			} `json:"event"`
		} `json:"payload"`
	}
	if json.Unmarshal(data, &raw) != nil {
		return Event{}, false
	}
	event := Event{Received: time.Now(), Count: 1}
	switch raw.Type {
	case "session_created", "session_removed", "session_destroyed":
		event.Kinds = eventKind(raw.Type)
		return event, true
	case "session_event":
		switch raw.Payload.Event.Type {
		case "session_view_changed":
			event.ViewOnly = true
			event.Kinds = eventKind("session_view_changed")
			return event, true
		case "block_closed", "block_created", "block_event":
			event.Kinds = eventKind(raw.Payload.Event.Type)
			return event, true
		}
	}
	return Event{}, false
}

// Only whitelisted type names are retained, never event payloads.
var eventTypes = [...]string{"session_view_changed", "block_event", "block_created", "block_closed", "session_created", "session_removed", "session_destroyed"}

func eventKind(name string) uint16 {
	for i, kind := range eventTypes {
		if name == kind {
			return 1 << i
		}
	}
	return 0
}

func (e Event) Types() []string {
	var names []string
	for i, name := range eventTypes {
		if e.Kinds&(1<<i) != 0 {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		if e.ViewOnly {
			return []string{"view_only"}
		}
		return []string{"unclassified_data"}
	}
	return names
}
