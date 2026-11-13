package manager

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"rex-statusline/internal/rex"
)

func focusEvent(events chan rex.Event) {
	events <- rex.Event{Received: time.Now(), ViewOnly: true}
}

func focusPane(f *fake, block string) {
	f.mu.Lock()
	f.view.Windows[0].Focused = block
	f.mu.Unlock()
}

func TestFocusRestoresCachedFrameWhileRevalidating(t *testing.T) {
	f := fixture()
	m := testManager(t, f)
	count := schedulerLog(t, m)
	var aCalls atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	m.Render = func(ctx context.Context, source Source) (string, error) {
		if source.Block.ID == "block:a" && aCalls.Add(1) > 1 {
			close(started)
			select {
			case <-release:
				return "updated a", nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		return source.Block.ID, nil
	}
	events, _, stop := startScheduler(t, m)
	eventually(t, func() bool { return hasFrame(m, "window:w", "block:a") })
	focusPane(f, "block:b")
	focusEvent(events)
	eventually(t, func() bool { return hasFrame(m, "window:w", "block:b") })
	focusPane(f, "block:a")
	focusEvent(events)
	<-started
	if !hasFrame(m, "window:w", "block:a") {
		t.Fatal("cached frame not restored before refresh")
	}
	// A layer/view notification must not discard this same-source refresh.
	focusEvent(events)
	eventually(t, func() bool { return count("render_reused", "event") == 1 })
	close(release)
	eventually(t, func() bool { return hasFrame(m, "window:w", "updated a") })
	stop()
	if count("frame_restored", "event") != 1 || count("render_discarded", "event") != 0 {
		t.Fatal("cache restoration or refresh supersession incorrect")
	}
}

func TestCachedRefreshCannotPublishAfterFocusMovesAway(t *testing.T) {
	f := fixture()
	m := testManager(t, f)
	count := schedulerLog(t, m)
	var calls atomic.Int32
	started := make(chan struct{})
	m.Render = func(ctx context.Context, source Source) (string, error) {
		if source.Block.ID == "block:a" && calls.Add(1) > 1 {
			close(started)
			<-ctx.Done()
			return "stale a", nil
		}
		return source.Block.ID, nil
	}
	events, _, stop := startScheduler(t, m)
	eventually(t, func() bool { return hasFrame(m, "window:w", "block:a") })
	focusPane(f, "block:b")
	focusEvent(events)
	eventually(t, func() bool { return hasFrame(m, "window:w", "block:b") })
	focusPane(f, "block:a")
	focusEvent(events)
	<-started
	focusPane(f, "block:b")
	focusEvent(events)
	eventually(t, func() bool { return hasFrame(m, "window:w", "block:b") })
	eventually(t, func() bool { return count("render_discarded", "event") == 1 })
	stop()
	rows := timingRows(t, m)
	canceled := requireLog(t, rows, "render_canceled", map[string]any{"block": "block:a", "reason": "source_changed"})
	requireLog(t, rows, "render_discarded", map[string]any{"update": canceled["update"], "reason": "superseded", "superseded_by": canceled["caused_by"]})
	if hasFrame(m, "window:w", "stale a") {
		t.Fatal("stale refresh published")
	}
}

func TestViewOnlyNotificationsReuseButDataEventsRefresh(t *testing.T) {
	f := fixture()
	m := testManager(t, f)
	count := schedulerLog(t, m)
	var calls atomic.Int32
	m.Render = func(context.Context, Source) (string, error) {
		if calls.Add(1) == 1 {
			return "initial", nil
		}
		return "fresh", nil
	}
	events, _, stop := startScheduler(t, m)
	eventually(t, func() bool { return hasFrame(m, "window:w", "initial") })
	for i := 1; i <= 3; i++ {
		focusEvent(events)
		eventually(t, func() bool { return count("render_reused", "event") == i })
	}
	if calls.Load() != 1 {
		t.Fatal("view-only notification rerendered data")
	}
	// A mixed batch must retain data invalidation.
	events <- rex.Event{Received: time.Now().Add(20 * time.Millisecond), ViewOnly: true}
	events <- rex.Event{Received: time.Now()}
	eventually(t, func() bool { return hasFrame(m, "window:w", "fresh") })
	stop()
	if calls.Load() != 2 {
		t.Fatalf("got %d renders, want 2", calls.Load())
	}
}

func TestFrameCacheInvalidation(t *testing.T) {
	f := fixture()
	m := testManager(t, f)
	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(m.frames) != 1 {
		t.Fatal("successful frame not cached")
	}
	sources, _ := Sources(f.view)
	source := sources["block:a"]
	old := m.frames[frameKey(source)]
	for _, change := range []func(*Source){
		func(s *Source) { s.Rect[2] /= 2 },
		func(s *Source) { s.Zoomed = !s.Zoomed },
		func(s *Source) { s.Block.HostID = "other" },
		func(s *Source) { s.SessionID = "other" },
	} {
		changed := source
		change(&changed)
		if _, ok := m.frames[frameKey(changed)]; ok {
			t.Fatal("reused incompatible cached frame")
		}
	}
	failed := old
	failed.line, failed.err = "bad", errors.New("failed")
	m.cacheFrame(context.Background(), failed)
	if m.frames[frameKey(source)].line != old.line {
		t.Fatal("failed refresh poisoned cache")
	}
	sources["block:a"] = Source{}
	m.pruneFrames(context.Background(), source.SessionID, sources)
	if len(m.frames) != 0 {
		t.Fatal("removed/changed pane retained cache")
	}
}

func TestGeometryChangeDoesNotRestoreCachedFrame(t *testing.T) {
	f := fixture()
	m := testManager(t, f)
	var aCalls atomic.Int32
	started := make(chan struct{})
	m.Render = func(ctx context.Context, source Source) (string, error) {
		if source.Block.ID == "block:a" && aCalls.Add(1) > 1 {
			close(started)
			<-ctx.Done()
			return "", ctx.Err()
		}
		return source.Block.ID, nil
	}
	events, _, stop := startScheduler(t, m)
	eventually(t, func() bool { return hasFrame(m, "window:w", "block:a") })
	focusPane(f, "block:b")
	focusEvent(events)
	eventually(t, func() bool { return hasFrame(m, "window:w", "block:b") })
	f.mu.Lock()
	f.view.Windows[0].Focused = "block:a"
	f.view.Windows[0].Layers[0].Layout.Split.Before.Rect[2] = .3
	f.mu.Unlock()
	focusEvent(events)
	<-started
	if !hasFrame(m, "window:w", "block:b") {
		t.Fatal("restored frame with obsolete pane width")
	}
	stop()
}

func TestRestoredFrameRevalidatesDimensions(t *testing.T) {
	f := fixture()
	m := testManager(t, f)
	count := schedulerLog(t, m)
	var aCalls atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	m.Render = func(ctx context.Context, source Source) (string, error) {
		if source.Block.ID == "block:a" && aCalls.Add(1) > 1 {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		return fmt.Sprintf("%s width %d", source.Block.ID, source.Size.Columns), nil
	}
	events, _, stop := startScheduler(t, m)
	eventually(t, func() bool { return hasFrame(m, "window:w", "block:a width 160") })
	focusPane(f, "block:b")
	focusEvent(events)
	eventually(t, func() bool { return hasFrame(m, "window:w", "block:b width 160") })
	f.mu.Lock()
	f.sizes = map[string]rex.Size{"block:a": {Columns: 40, Rows: 24}}
	f.view.Windows[0].Focused = "block:a"
	f.mu.Unlock()
	focusEvent(events)
	<-started
	if !hasFrame(m, "window:w", "block:a width 160") {
		t.Fatal("missing provisional restoration")
	}
	close(release)
	eventually(t, func() bool { return hasFrame(m, "window:w", "block:a width 80") })
	eventually(t, func() bool { return count("render_done", "event") == 2 })
	stop()
	for key, frame := range m.frames {
		if key.Block.ID == "block:a" && frame.source.Size.Columns != 40 {
			t.Fatal("obsolete dimensions retained after revalidation")
		}
	}
}
