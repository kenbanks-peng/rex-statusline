package manager

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"rex-statusline/internal/rex"
)

func TestReopenedWindowWaitsForCanceledWorker(t *testing.T) {
	f := fixture()
	m := testManager(t, f)
	count := schedulerLog(t, m)
	saved := f.view.Windows[0]
	started, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	m.Render = func(ctx context.Context, _ Source) (string, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-ctx.Done()
			close(canceled)
			<-release
			return "obsolete", nil
		}
		return "reopened", nil
	}
	events, _, stop := startScheduler(t, m)
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	<-started
	f.mu.Lock()
	f.view.Windows = nil
	f.mu.Unlock()
	events <- rex.Event{Received: time.Now()}
	<-canceled
	f.mu.Lock()
	f.view.Windows = []rex.Window{saved}
	f.mu.Unlock()
	events <- rex.Event{Received: time.Now()}
	eventually(t, func() bool { return count("render_queued", "event") == 1 })
	if calls.Load() != 1 {
		t.Fatal("reopened window overlapped its canceled render")
	}
	close(release)
	eventually(t, func() bool { return hasFrame(m, "window:w", "reopened") })
	stop()
	if calls.Load() != 2 {
		t.Fatalf("renders: %d", calls.Load())
	}
	if count("frame_published", "startup") != 0 {
		t.Fatal("obsolete frame was published")
	}
}
