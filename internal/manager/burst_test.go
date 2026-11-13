package manager

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"rex-statusline/internal/rex"
)

func TestCloselySpacedEventsShareSnapshot(t *testing.T) {
	f := fixture()
	m := testManager(t, f)
	count := schedulerLog(t, m)
	var views atomic.Int32
	m.API = apiFunc(func(ctx context.Context, s, method string, args, out any) error {
		if method == "session.view" {
			views.Add(1)
		}
		return f.Call(ctx, s, method, args, out)
	})
	events, _, stop := startScheduler(t, m)
	eventually(t, func() bool { return hasFrame(m, "window:w", "status for block:a") })
	// A future arrival supplies generous deterministic headroom for scheduling
	// this test's second notification inside the same fixed collection interval.
	events <- rex.Event{Received: time.Now().Add(30 * time.Millisecond)}
	time.Sleep(time.Millisecond)
	f.mu.Lock()
	f.view.Windows[0].Focused = "block:b"
	f.mu.Unlock()
	events <- rex.Event{Received: time.Now()}
	eventually(t, func() bool { return hasFrame(m, "window:w", "status for block:b") })
	stop()
	if views.Load() != 2 || count("snapshot_done", "event") != 1 {
		t.Fatalf("views=%d event snapshots=%d", views.Load(), count("snapshot_done", "event"))
	}
}

func TestEventCollectionStopsOnCancellation(t *testing.T) {
	f := fixture()
	m := testManager(t, f)
	count := schedulerLog(t, m)
	events, _, stop := startScheduler(t, m)
	eventually(t, func() bool { return hasFrame(m, "window:w", "status for block:a") })
	events <- rex.Event{Received: time.Now().Add(time.Hour)}
	stop()
	if count("snapshot_done", "event") != 0 {
		t.Fatal("canceled collection performed discovery")
	}
}
