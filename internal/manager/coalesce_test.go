package manager

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"rex-statusline/internal/rex"
	"rex-statusline/internal/timing"
)

func TestQueuedBurstNeedsOneFreshSnapshot(t *testing.T) {
	f := fixture()
	m := testManager(t, f)
	count := schedulerLog(t, m)
	read, release := make(chan struct{}), make(chan struct{})
	var views atomic.Int32
	m.API = apiFunc(func(ctx context.Context, session, method string, args, out any) error {
		err := f.Call(ctx, session, method, args, out)
		if method == "session.view" && views.Add(1) == 1 {
			close(read)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return err
	})
	events, _, stop := startScheduler(t, m)
	<-read
	f.mu.Lock()
	f.view.Windows[0].Focused = "block:b"
	f.mu.Unlock()
	for range 8 {
		events <- rex.Event{Received: time.Now()}
	}
	close(release)
	eventually(t, func() bool { return hasFrame(m, "window:w", "status for block:b") })
	stop()
	if views.Load() != 2 || count("snapshot_done", "event") != 1 {
		t.Fatalf("views %d event snapshots %d; want startup + one burst", views.Load(), count("snapshot_done", "event"))
	}
}

func TestEventAfterViewReadForcesAnotherSnapshot(t *testing.T) {
	f := fixture()
	m := testManager(t, f)
	count := schedulerLog(t, m)
	var renders atomic.Int32
	originalRender := m.Render
	m.Render = func(ctx context.Context, source Source) (string, error) {
		renders.Add(1)
		return originalRender(ctx, source)
	}
	read, release := make(chan struct{}), make(chan struct{})
	var views atomic.Int32
	m.API = apiFunc(func(ctx context.Context, session, method string, args, out any) error {
		err := f.Call(ctx, session, method, args, out)
		if method == "session.view" && views.Add(1) == 2 {
			// The first event's snapshot has already read the OLD focused source.
			close(read)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return err
	})
	events, _, stop := startScheduler(t, m)
	eventually(t, func() bool { return hasFrame(m, "window:w", "status for block:a") })
	events <- rex.Event{Received: time.Now()}
	<-read
	f.mu.Lock()
	f.view.Windows[0].Focused = "block:b"
	f.mu.Unlock()
	events <- rex.Event{Received: time.Now()}
	close(release)
	eventually(t, func() bool { return hasFrame(m, "window:w", "status for block:b") })
	stop()
	if count("snapshot_done", "event") != 2 {
		t.Fatalf("event during snapshot lost: snapshots %d", count("snapshot_done", "event"))
	}
	if renders.Load() != 2 {
		t.Fatalf("rendered superseded discovery: %d calls, want initial + latest", renders.Load())
	}
}

func TestEventBurstSkipsPreferenceProcessesButPollRefreshes(t *testing.T) {
	f := fixture()
	m := testManager(t, f)
	var reads atomic.Int32
	padding := 2.0
	m.Padding = func(context.Context) (*float64, error) { reads.Add(1); return &padding, nil }
	m.HorizontalPadding = m.Padding
	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	for range 5 {
		ctx := timing.WithTrace(context.Background(), timing.New("event", time.Now()))
		if err := m.Reconcile(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if reads.Load() != 2 {
		t.Fatalf("event preference reads %d", reads.Load())
	}
	ctx := timing.WithTrace(context.Background(), timing.New("poll", time.Now()))
	if err := m.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if reads.Load() != 4 {
		t.Fatalf("poll did not refresh preferences: %d", reads.Load())
	}
}

func TestClosedEventStreamDoesNotSpin(t *testing.T) {
	f := fixture()
	m := testManager(t, f)
	events, ticks, stop := startScheduler(t, m)
	eventually(t, func() bool { return hasFrame(m, "window:w", "status for block:a") })
	close(events)
	f.mu.Lock()
	f.view.Windows[0].Focused = "block:b"
	f.mu.Unlock()
	ticks <- time.Now()
	eventually(t, func() bool { return hasFrame(m, "window:w", "status for block:b") })
	stop()
}
