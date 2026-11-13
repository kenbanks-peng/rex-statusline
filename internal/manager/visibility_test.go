package manager

import (
	"context"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"rex-statusline/internal/rex"
)

func addInactiveTab(f *fake) {
	f.view.Windows = append(f.view.Windows, rex.Window{
		ID: "window:other", Focused: "block:c",
		Layers: []rex.Layer{{ID: "layer:other", Kind: "tiled",
			Blocks: []rex.Block{{ID: "block:c", Creator: rex.Terminal}},
			Layout: &rex.Node{BlockID: "block:c", Rect: rex.Rect{0, 0, 1, 1}},
		}},
	})
}

func TestInactiveTabDoesNoRenderOrSizeWorkOnEventsAndPolls(t *testing.T) {
	f := fixture()
	m := testManager(t, f)
	addInactiveTab(f)
	count := schedulerLog(t, m)
	var hiddenCalls atomic.Int32
	m.Render = func(_ context.Context, source Source) (string, error) {
		if source.WindowID == "window:other" {
			hiddenCalls.Add(1)
		}
		return source.Block.ID, nil
	}
	events, ticks, stop := startScheduler(t, m)
	eventually(t, func() bool { return hasFrame(m, "window:w", "block:a") })
	for i := 1; i <= 3; i++ {
		events <- rex.Event{Received: time.Now()}
		eventually(t, func() bool { return count("render_done", "event") == i })
		ticks <- time.Now()
		eventually(t, func() bool { return count("render_done", "poll") == i })
	}
	stop()
	if hiddenCalls.Load() != 0 {
		t.Fatal("rendered inactive tab")
	}
	for _, block := range f.sizeRequests {
		if block != "block:a" {
			t.Fatalf("queried size of inactive block %s", block)
		}
	}
	if _, err := os.Stat(m.FramePath("window:other")); !os.IsNotExist(err) {
		t.Fatal("published inactive tab")
	}
}

func TestTabDeactivationCancelsRenderAndActivationRefreshes(t *testing.T) {
	for _, active := range []string{"window:other", ""} {
		t.Run(active, func(t *testing.T) {
			f := fixture()
			m := testManager(t, f)
			addInactiveTab(f)
			count := schedulerLog(t, m)
			started, canceled := make(chan struct{}), make(chan struct{})
			var calls atomic.Int32
			m.Render = func(ctx context.Context, source Source) (string, error) {
				if source.WindowID == "window:w" && calls.Add(1) == 1 {
					close(started)
					<-ctx.Done()
					close(canceled)
					// Success after cancellation must not publish a stale frame.
					return "stale", nil
				}
				return source.Block.ID, nil
			}
			events, _, stop := startScheduler(t, m)
			<-started
			f.mu.Lock()
			f.view.ActiveWindowID = active
			f.mu.Unlock()
			events <- rex.Event{Received: time.Now()}
			select {
			case <-canceled:
			case <-time.After(3 * time.Second):
				t.Fatal("inactive render was not canceled")
			}
			eventually(t, func() bool { return count("render_discarded", "startup") == 1 })
			if _, err := os.Stat(m.FramePath("window:w")); !os.IsNotExist(err) {
				t.Fatal("inactive render published after cancellation")
			}
			f.mu.Lock()
			f.view.ActiveWindowID = "window:w"
			f.view.Windows[0].Focused = "block:b"
			f.mu.Unlock()
			events <- rex.Event{Received: time.Now()}
			eventually(t, func() bool { return hasFrame(m, "window:w", "block:b") })
			stop()
			if calls.Load() != 2 {
				t.Fatalf("renders %d, want canceled + reactivated", calls.Load())
			}
		})
	}
}
