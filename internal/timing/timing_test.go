package timing

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCorrelatedRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "timing.jsonl")
	logger, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	trace := New("event", time.Now().Add(-50*time.Millisecond))
	trace.Session, trace.Block, trace.Phase = "session:test", "block:test", "restore"
	ctx := WithTrace(context.Background(), trace)
	if From(ctx) != trace {
		t.Fatal("trace not preserved")
	}
	logger.Record(From(ctx), "window:test", "render_done", time.Now().Add(-20*time.Millisecond), "ok", true)
	if err := logger.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var row map[string]any
	if err := json.Unmarshal(data, &row); err != nil {
		t.Fatal(err)
	}
	if row["update"] != trace.ID || row["msg"] != "render_done" || row["window"] != "window:test" || row["trigger"] != "event" {
		t.Fatalf("%v", row)
	}
	if row["session"] != trace.Session || row["block"] != trace.Block || row["phase"] != trace.Phase {
		t.Fatalf("missing pane/phase correlation: %v", row)
	}
	if row["elapsed_ms"].(float64) < 50 || row["duration_ms"].(float64) < 20 {
		t.Fatalf("%v", row)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("log permissions: %v %v", info, err)
	}
}

func TestDisabled(t *testing.T) {
	logger, err := Open("")
	if err != nil || logger != nil {
		t.Fatal(logger, err)
	}
	logger.Record(Trace{}, "", "ignored", time.Time{})
	if err := logger.Close(); err != nil {
		t.Fatal(err)
	}
}
