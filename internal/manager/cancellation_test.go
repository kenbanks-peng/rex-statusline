package manager

import (
	"context"
	"testing"
	"time"

	"rex-statusline/internal/rex"
)

func TestSourceChangeCancelsObsoleteRender(t *testing.T) {
	f := fixture()
	m := testManager(t, f)
	started, cancelled := make(chan struct{}), make(chan struct{})
	m.Render = func(ctx context.Context, source Source) (string, error) {
		if source.Block.ID == "block:a" {
			close(started)
			<-ctx.Done()
			close(cancelled)
			return "stale", nil
		}
		return "latest", nil
	}
	events, _, stop := startScheduler(t, m)
	<-started
	f.mu.Lock()
	f.view.Windows[0].Focused = "block:b"
	f.mu.Unlock()
	events <- rex.Event{Received: time.Now()}
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("obsolete render was not cancelled")
	}
	eventually(t, func() bool { return hasFrame(m, "window:w", "latest") })
	stop()
}

func TestSameSourceEventDoesNotCancelActiveRender(t *testing.T) {
	f := fixture()
	m := testManager(t, f)
	count := schedulerLog(t, m)
	started, release := make(chan struct{}), make(chan struct{})
	first := true
	m.Render = func(ctx context.Context, _ Source) (string, error) {
		if first {
			first = false
			close(started)
			select {
			case <-ctx.Done():
				t.Error("same-source event cancelled active render")
				return "", ctx.Err()
			case <-release:
			}
		}
		return "latest", nil
	}
	events, _, stop := startScheduler(t, m)
	<-started
	events <- rex.Event{Received: time.Now()}
	eventually(t, func() bool { return count("render_queued", "event") == 1 })
	close(release)
	eventually(t, func() bool { return hasFrame(m, "window:w", "latest") })
	stop()
}
