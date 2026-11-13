package manager

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"rex-statusline/internal/presentation"
	"rex-statusline/internal/rex"
	"rex-statusline/internal/timing"
)

func TestRunCommitsCompletedFrameWithoutDebounce(t *testing.T) {
	f := fixture()
	m := testManager(t, f)
	timingPath := filepath.Join(t.TempDir(), "timing.jsonl")
	logger, err := timing.Open(timingPath)
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()
	m.Timing = logger
	var line atomic.Value
	line.Store("initial")
	var renders atomic.Int32
	started := make(chan struct{}, 10)
	release := make(chan struct{})
	m.Render = func(ctx context.Context, _ Source) (string, error) {
		renders.Add(1)
		value := line.Load().(string)
		if value != "initial" {
			started <- struct{}{}
			select {
			case <-release:
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		return value, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	events := make(chan rex.Event, 10)
	go func() { done <- m.Run(ctx, events) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	read := func() string {
		data, err := os.ReadFile(m.FramePath("window:w"))
		if err != nil {
			return ""
		}
		var frame presentation.Frame
		if err := json.Unmarshal(data, &frame); err != nil {
			t.Fatal(err)
		}
		return frame.Line
	}
	wait := func(want string) {
		t.Helper()
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			if read() == want {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for %q; got %q", want, read())
	}
	wait("initial")
	line.Store("a substantially longer next frame")
	received := time.Now().Add(-20 * time.Millisecond)
	events <- rex.Event{Received: received}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("event burst was not rendered")
	}
	if got := read(); got != "initial" {
		t.Fatalf("incomplete render leaked: %q", got)
	}
	close(release)
	wait("a substantially longer next frame")
	select {
	case <-started:
		t.Fatal("completed frame was rendered again")
	case <-time.After(200 * time.Millisecond):
	}
	data, err := os.ReadFile(m.FramePath("window:w"))
	if err != nil {
		t.Fatal(err)
	}
	var frame presentation.Frame
	if err := json.Unmarshal(data, &frame); err != nil {
		t.Fatal(err)
	}
	if frame.Trace == nil || !frame.Trace.Started.Equal(received) {
		t.Fatalf("event arrival not propagated: %+v", frame)
	}
	records, err := os.ReadFile(timingPath)
	if err != nil {
		t.Fatal(err)
	}
	stages := map[string]bool{}
	for _, row := range strings.Split(strings.TrimSpace(string(records)), "\n") {
		var entry map[string]any
		if err := json.Unmarshal([]byte(row), &entry); err != nil {
			t.Fatal(err)
		}
		if entry["update"] != frame.Trace.ID {
			continue
		}
		stage := entry["msg"].(string)
		stages[stage] = true
		if stage == "event_dequeued" && entry["queue_ms"].(float64) < 20 {
			t.Fatal(entry)
		}
		if stage == "render_started" && entry["elapsed_ms"].(float64) < 20 {
			t.Fatal(entry)
		}
	}
	for _, stage := range []string{"event_dequeued", "snapshot_started", "render_started", "render_done", "frame_published"} {
		if !stages[stage] {
			t.Errorf("missing stage %s", stage)
		}
	}
	if got := renders.Load(); got != 2 {
		t.Fatalf("render calls: %d, want startup + one update", got)
	}
}
