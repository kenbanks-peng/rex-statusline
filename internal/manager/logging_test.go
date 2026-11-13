package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"rex-statusline/internal/presentation"
	"rex-statusline/internal/rex"
	"rex-statusline/internal/timing"
)

func timingRows(t *testing.T, m *Manager) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(m.Timing.Path)
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
		var row map[string]any
		if err := json.Unmarshal(line, &row); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row)
	}
	return rows
}

func requireLog(t *testing.T, rows []map[string]any, stage string, fields map[string]any) map[string]any {
	t.Helper()
	for _, row := range rows {
		if row["msg"] != stage {
			continue
		}
		matches := true
		for key, value := range fields {
			if row[key] != value {
				matches = false
				break
			}
		}
		if matches {
			return row
		}
	}
	t.Fatalf("missing %s with %v", stage, fields)
	return nil
}

func TestFocusLoggingCorrelatesRestorationAndRefresh(t *testing.T) {
	f := fixture()
	m := testManager(t, f)
	count := schedulerLog(t, m)
	f.view.Windows[0].Layers[0].Blocks[0].Label = "PRIVATE_LABEL"
	m.Render = func(_ context.Context, source Source) (string, error) {
		return "PRIVATE_CONTENT " + source.Block.ID, nil
	}
	events, _, stop := startScheduler(t, m)
	eventually(t, func() bool { return hasFrame(m, "window:w", "PRIVATE_CONTENT block:a") })
	focusEvent(events)
	eventually(t, func() bool { return count("render_reused", "event") == 1 })
	focusPane(f, "block:b")
	focusEvent(events)
	eventually(t, func() bool { return hasFrame(m, "window:w", "PRIVATE_CONTENT block:b") })
	focusPane(f, "block:a")
	focusEvent(events)
	eventually(t, func() bool { return count("refresh_compared", "event") == 1 })
	eventually(t, func() bool { return count("frame_cache_stored", "event") == 2 })
	stop()
	rows := timingRows(t, m)
	restored := requireLog(t, rows, "frame_restored", map[string]any{"block": "block:a", "phase": "restore"})
	update := restored["update"]
	for _, stage := range []string{"source_selected", "frame_cache_lookup", "frame_restore_started", "frame_restore_done", "render_queued", "render_launched", "refresh_started", "refresh_done", "render_received", "refresh_compared", "frame_cache_stored"} {
		row := requireLog(t, rows, stage, map[string]any{"update": update, "block": "block:a", "session": "session:s", "window": "window:w"})
		if row["elapsed_ms"] == nil {
			t.Fatalf("%s missing elapsed time", stage)
		}
	}
	lookup := requireLog(t, rows, "frame_cache_lookup", map[string]any{"update": update, "hit": true})
	if lookup["cached_update"] == update || lookup["cached_update"] == "" || lookup["cache_age_ms"].(float64) < 0 {
		t.Fatal("missing cache provenance/age")
	}
	requireLog(t, rows, "frame_cache_lookup", map[string]any{"hit": false, "reason": "not_cached"})
	requireLog(t, rows, "source_selected", map[string]any{"update": update, "previous_block": "block:b", "reason": "focus_changed"})
	requireLog(t, rows, "render_reused", map[string]any{"reason": "view_only_same_source"})
	requireLog(t, rows, "event_dequeued", map[string]any{"classification": "view_only"})
	requireLog(t, rows, "frame_published", map[string]any{"update": update, "phase": "restore", "reason": "content_changed"})
	requireLog(t, rows, "frame_unchanged", map[string]any{"update": update, "phase": "refresh"})
	requireLog(t, rows, "refresh_compared", map[string]any{"update": update, "content_changed": false, "dimensions_changed": false})
	for _, stage := range []string{"frame_restore_done", "refresh_done", "commit_done", "render_received"} {
		row := requireLog(t, rows, stage, map[string]any{"update": update})
		duration, ok := row["duration_ms"].(float64)
		if !ok || duration < 0 {
			t.Fatalf("%s missing duration", stage)
		}
	}
	encoded, err := os.ReadFile(m.FramePath("window:w"))
	if err != nil {
		t.Fatal(err)
	}
	var frame presentation.Frame
	if err := json.Unmarshal(encoded, &frame); err != nil {
		t.Fatal(err)
	}
	if frame.Trace == nil || frame.Trace.ID != update || frame.Trace.Block != "block:a" || frame.Trace.Session != "session:s" || frame.Trace.Phase != "restore" {
		t.Fatal("display metadata lost pane/restore correlation")
	}
	data, _ := os.ReadFile(m.Timing.Path)
	if strings.Contains(string(data), "PRIVATE_") {
		t.Fatal("timing log contains status content or pane labels")
	}
}

func TestCacheInvalidationLogging(t *testing.T) {
	for _, reason := range []string{"pane_removed", "geometry_or_visibility_changed", "source_changed", "session_removed"} {
		t.Run(reason, func(t *testing.T) {
			f := fixture()
			m := testManager(t, f)
			schedulerLog(t, m)
			source := Source{SessionID: "s", WindowID: "w", Block: rex.Block{ID: "b", HostID: "h"}, Rect: rex.Rect{0, 0, 1, 1}, Size: rex.Size{Columns: 80, Rows: 24}}
			seed := timing.New("event", time.Now().Add(-time.Second))
			m.cacheFrame(timing.WithTrace(context.Background(), seed), renderedFrame{source: source, line: "PRIVATE_CONTENT"})
			trace := timing.New("event", time.Now())
			ctx := timing.WithTrace(context.Background(), trace)
			key := frameKey(source)
			sources := map[string]Source{source.Block.ID: key}
			switch reason {
			case "pane_removed":
				delete(sources, source.Block.ID)
			case "geometry_or_visibility_changed":
				changed := key
				changed.Rect[2] = .5
				sources[source.Block.ID] = changed
			case "source_changed":
				changed := key
				changed.Block.HostID = "other"
				sources[source.Block.ID] = changed
			}
			if reason == "session_removed" {
				m.invalidateFrame(ctx, key, reason)
			} else {
				m.pruneFrames(ctx, source.SessionID, sources)
			}
			row := requireLog(t, timingRows(t, m), "frame_cache_invalidated", map[string]any{"update": trace.ID, "block": "b", "reason": reason, "cached_update": seed.ID})
			if row["cache_age_ms"].(float64) < 0 {
				t.Fatal("invalid cache age")
			}
		})
	}
}

func TestRestoreFailureLogging(t *testing.T) {
	f := fixture()
	m := testManager(t, f)
	count := schedulerLog(t, m)
	m.Render = func(_ context.Context, source Source) (string, error) {
		if source.Block.ID == "block:a" {
			return "a much longer cached frame", nil
		}
		return "b", nil
	}
	var failResize bool
	m.API = apiFunc(func(ctx context.Context, session, method string, args, out any) error {
		if method == "session.resize_layer" && failResize {
			return os.ErrPermission
		}
		return f.Call(ctx, session, method, args, out)
	})
	// Use synchronous setup so the failure flag is published before Run starts.
	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	focusPane(f, "block:b")
	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	failResize = true
	focusPane(f, "block:a")
	// A new scheduler restores cached A before it starts revalidation.
	_, _, stop := startScheduler(t, m)
	eventually(t, func() bool { return count("frame_restore_done", "startup") == 1 })
	stop()
	requireLog(t, timingRows(t, m), "frame_restore_done", map[string]any{"block": "block:a", "phase": "restore", "ok": false})
}
