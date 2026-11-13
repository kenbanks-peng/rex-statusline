package rex

import (
	"context"
	"testing"
	"time"
)

func TestEventClassification(t *testing.T) {
	for _, test := range []struct {
		raw            string
		relevant, view bool
	}{
		{`{"type":"session_event","payload":{"event":{"type":"session_view_changed"}}}`, true, true},
		{`{"type":"session_event","payload":{"event":{"type":"block_event","payload":{"event":"anything"}}}}`, true, false},
		{`{"type":"session_event","payload":{"event":{"type":"block_created"}}}`, true, false},
		{`{"type":"session_event","payload":{"event":{"type":"block_closed"}}}`, true, false},
		{`{"type":"session_destroyed"}`, true, false},
		{`{"type":"session_removed"}`, true, false},
		{`{"type":"session_created"}`, true, false},
		{`{"type":"session_event","payload":{"event":{"type":"other"}}}`, false, false},
		{`{"type":"other"}`, false, false},
		{`bad json`, false, false},
	} {
		event, relevant := parseEvent([]byte(test.raw))
		if relevant != test.relevant || event.ViewOnly != test.view {
			t.Fatalf("%s: relevant=%v view=%v", test.raw, relevant, event.ViewOnly)
		}
	}
}

func TestFullEventQueuePreservesDataInvalidationAndArrival(t *testing.T) {
	events := make(chan Event, 1)
	first := time.Now().Add(-time.Second)
	enqueueEvent(context.Background(), events, Event{Received: first, ViewOnly: true})
	enqueueEvent(context.Background(), events, Event{Received: time.Now(), ViewOnly: false})
	enqueueEvent(context.Background(), events, Event{Received: time.Now(), ViewOnly: true})
	event := <-events
	if event.ViewOnly || event.Received != first || event.Count != 3 {
		t.Fatalf("lost data invalidation, first arrival, or count: %+v", event)
	}
}

func TestEventMergeOrder(t *testing.T) {
	older := Event{Received: time.Now().Add(-time.Second), ViewOnly: true}
	newer := Event{Received: time.Now(), Count: 3}
	a, b := older.Merge(newer), newer.Merge(older)
	if a != b || a.ViewOnly || a.Count != 4 || a.Received != older.Received {
		t.Fatalf("merge order changes semantics: %+v %+v", a, b)
	}
}

func TestMergedEventTypes(t *testing.T) {
	view, _ := parseEvent([]byte(`{"type":"session_event","payload":{"event":{"type":"session_view_changed"}}}`))
	data, _ := parseEvent([]byte(`{"type":"session_event","payload":{"event":{"type":"block_event","payload":{"private":"not logged"}}}}`))
	merged := view.Merge(data).Merge(view)
	types := merged.Types()
	if len(types) != 2 || types[0] != "session_view_changed" || types[1] != "block_event" || merged.ViewOnly {
		t.Fatalf("lost event classifications: %+v %v", merged, types)
	}
}
