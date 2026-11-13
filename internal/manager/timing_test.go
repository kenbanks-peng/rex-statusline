package manager

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"rex-statusline/internal/presentation"
	"rex-statusline/internal/timing"
)

func TestTimingCorrelatesFramesWithoutRepublishingUnchangedContent(t *testing.T) {
	f := fixture()
	m := testManager(t, f)
	path := filepath.Join(t.TempDir(), "timing.jsonl")
	logger, err := timing.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()
	m.Timing = logger
	line := "PRIVATE_CONTENT"
	m.Render = func(context.Context, Source) (string, error) { time.Sleep(10 * time.Millisecond); return line, nil }
	trace := timing.New("event", time.Now().Add(-150*time.Millisecond))
	ctx := timing.WithTrace(context.Background(), trace)
	if err := m.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(m.FramePath("window:w"))
	if err != nil {
		t.Fatal(err)
	}
	var frame presentation.Frame
	if err := json.Unmarshal(data, &frame); err != nil {
		t.Fatal(err)
	}
	if frame.Trace == nil || frame.Trace.ID != trace.ID || frame.Trace.Window != "window:w" {
		t.Fatalf("%+v", frame)
	}
	// A different update ID must not cause filesystem events for identical data.
	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	unchanged, err := os.ReadFile(m.FramePath("window:w"))
	if err != nil || string(data) != string(unchanged) {
		t.Fatal("trace metadata republished unchanged content", err)
	}
	line = "a longer PRIVATE_CONTENT frame"
	if err := m.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	log, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(log), "PRIVATE_CONTENT") {
		t.Fatal("content leaked into timings")
	}
	stages := map[string]bool{}
	for _, row := range strings.Split(strings.TrimSpace(string(log)), "\n") {
		var event map[string]any
		if err := json.Unmarshal([]byte(row), &event); err != nil {
			t.Fatal(err)
		}
		stage := event["msg"].(string)
		stages[stage] = true
		if stage == "render_done" && event["duration_ms"].(float64) < 10 {
			t.Fatalf("render measurement: %v", event)
		}
	}
	for _, stage := range []string{"snapshot_started", "size_done", "render_started", "render_done", "frame_published", "frame_unchanged", "create_started", "create_done", "resize_started", "resize_done", "snapshot_done"} {
		if !stages[stage] {
			t.Errorf("missing %s", stage)
		}
	}
}
