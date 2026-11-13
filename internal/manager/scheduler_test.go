package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"rex-statusline/internal/rex"
	"rex-statusline/internal/timing"
)

func eventually(t *testing.T, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition did not become true")
}

func hasFrame(m *Manager, window, line string) bool {
	data, err := os.ReadFile(m.FramePath(window))
	if err != nil {
		return false
	}
	var frame struct{ Line string }
	return json.Unmarshal(data, &frame) == nil && frame.Line == line
}

func addTestSessions(m *Manager, f *fake, count int) {
	sessions := sessionAPI{f.view.SessionID: f}
	for i := 1; i < count; i++ {
		other := fixture()
		other.setLeaves()
		other.view.SessionID = fmt.Sprintf("session:%d", i)
		other.view.Windows[0].ID = fmt.Sprintf("window:%d", i)
		other.view.ActiveWindowID = other.view.Windows[0].ID
		sessions[other.view.SessionID] = other
	}
	m.API = sessions
}

func schedulerLog(t *testing.T, m *Manager) func(string, string) int {
	t.Helper()
	path := filepath.Join(t.TempDir(), "timing.jsonl")
	logger, err := timing.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	m.Timing = logger
	t.Cleanup(func() { logger.Close() })
	return func(stage, trigger string) int {
		data, _ := os.ReadFile(path)
		count := 0
		for _, row := range bytes.Split(data, []byte("\n")) {
			var entry struct{ Msg, Trigger string }
			if json.Unmarshal(row, &entry) == nil && entry.Msg == stage && entry.Trigger == trigger {
				count++
			}
		}
		return count
	}
}

func startScheduler(t *testing.T, m *Manager) (chan rex.Event, chan time.Time, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	events, ticks := make(chan rex.Event, 32), make(chan time.Time, 8)
	done := make(chan error, 1)
	go func() { done <- m.run(ctx, events, ticks) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Error(err)
				}
			case <-time.After(3 * time.Second):
				t.Error("scheduler did not stop")
			}
		})
	}
	t.Cleanup(stop)
	return events, ticks, stop
}

func TestWindowsPublishIndependently(t *testing.T) {
	for _, asynchronous := range []bool{false, true} {
		t.Run(fmt.Sprint(asynchronous), func(t *testing.T) {
			f := fixture()
			m := testManager(t, f)
			addTestSessions(m, f, 2)
			slow := make(chan struct{})
			m.Render = func(ctx context.Context, source Source) (string, error) {
				if source.WindowID == "window:w" {
					select {
					case <-slow:
					case <-ctx.Done():
						return "", ctx.Err()
					}
				}
				return source.WindowID, nil
			}
			if asynchronous {
				startScheduler(t, m)
			} else {
				ctx, cancel := context.WithCancel(context.Background())
				done := make(chan error, 1)
				go func() { done <- m.Reconcile(ctx) }()
				t.Cleanup(func() { cancel(); <-done })
			}
			eventually(t, func() bool { return hasFrame(m, "window:1", "window:1") })
			if _, err := os.Stat(m.FramePath("window:w")); !os.IsNotExist(err) {
				t.Fatal("slow window completed before release")
			}
			close(slow)
			eventually(t, func() bool { return hasFrame(m, "window:w", "window:w") })
		})
	}
}

func TestSchedulerCoalescesPendingWorkAndDiscardsStaleResult(t *testing.T) {
	f := fixture()
	m := testManager(t, f)
	count := schedulerLog(t, m)
	var calls atomic.Int32
	firstStarted := make(chan struct{})
	release := make(chan struct{})
	m.Render = func(ctx context.Context, source Source) (string, error) {
		if calls.Add(1) == 1 {
			close(firstStarted)
			// Model a renderer that cannot stop immediately on cancellation.
			<-release
		}
		return source.Block.ID, nil
	}
	events, _, stop := startScheduler(t, m)
	<-firstStarted
	// Several snapshots must replace one pending request, not enqueue more workers.
	for i := 1; i <= 4; i++ {
		f.mu.Lock()
		if i%2 == 0 {
			f.view.Windows[0].Focused = "block:b"
		} else {
			f.view.Windows[0].Focused = "block:a"
		}
		f.mu.Unlock()
		events <- rex.Event{Received: time.Now()}
		eventually(t, func() bool { return count("render_queued", "event") >= i })
	}
	if calls.Load() != 1 {
		t.Fatal("overlapping renders in one window")
	}
	close(release)
	eventually(t, func() bool { return hasFrame(m, "window:w", "block:b") })
	stop()
	if calls.Load() != 2 {
		t.Fatalf("renders: %d, want active + latest", calls.Load())
	}
	if count("render_discarded", "startup") != 1 {
		t.Fatal("stale startup frame was not discarded")
	}
	if count("frame_published", "startup") != 0 {
		t.Fatal("stale content was published")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.created != 1 {
		t.Fatalf("created %d floats", f.created)
	}
}

func TestPollDoesNotSupersedeActiveEvent(t *testing.T) {
	f := fixture()
	m := testManager(t, f)
	count := schedulerLog(t, m)
	var calls atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	m.Render = func(ctx context.Context, _ Source) (string, error) {
		if calls.Add(1) == 1 {
			return "initial", nil
		}
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
			return "", ctx.Err()
		}
		return "event", nil
	}
	events, ticks, stop := startScheduler(t, m)
	eventually(t, func() bool { return hasFrame(m, "window:w", "initial") })
	events <- rex.Event{Received: time.Now()}
	<-started
	ticks <- time.Now()
	eventually(t, func() bool { return count("snapshot_done", "poll") == 1 })
	close(release)
	eventually(t, func() bool { return hasFrame(m, "window:w", "event") })
	stop()
	if calls.Load() != 2 {
		t.Fatalf("renders: %d", calls.Load())
	}
	if count("render_discarded", "event") != 0 {
		t.Fatal("poll superseded event")
	}
}

func TestRemovedWindowCannotPublishCompletedRender(t *testing.T) {
	f := fixture()
	m := testManager(t, f)
	count := schedulerLog(t, m)
	started := make(chan struct{})
	m.Render = func(ctx context.Context, _ Source) (string, error) {
		close(started)
		<-ctx.Done()
		// Even a renderer returning success after cancellation cannot resurrect a tab.
		return "removed", nil
	}
	events, _, stop := startScheduler(t, m)
	<-started
	f.mu.Lock()
	f.view.Windows = nil
	f.mu.Unlock()
	events <- rex.Event{Received: time.Now()}
	eventually(t, func() bool { return count("render_discarded", "startup") == 1 })
	stop()
	if _, err := os.Stat(m.FramePath("window:w")); !os.IsNotExist(err) {
		t.Fatal("removed window published a frame")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.created != 0 {
		t.Fatal("removed window created a float")
	}
}

func TestSchedulerBoundsWorkersAndJoinsOnShutdown(t *testing.T) {
	f := fixture()
	m := testManager(t, f)
	addTestSessions(m, f, 8)
	var active, exited atomic.Int32
	m.Render = func(ctx context.Context, _ Source) (string, error) {
		active.Add(1)
		defer func() { active.Add(-1); exited.Add(1) }()
		<-ctx.Done()
		return "", ctx.Err()
	}
	_, _, stop := startScheduler(t, m)
	eventually(t, func() bool { return active.Load() == 4 })
	stop()
	if active.Load() != 0 || exited.Load() != 4 {
		t.Fatalf("active %d exited %d", active.Load(), exited.Load())
	}
}
