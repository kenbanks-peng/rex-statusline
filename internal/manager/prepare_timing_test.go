package manager

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"rex-statusline/internal/rex"
	"rex-statusline/internal/timing"
)

func TestPrepareTimingKeepsWindow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "timing.jsonl")
	logger, err := timing.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()
	m := Manager{
		Timing: logger,
		API: apiFunc(func(_ context.Context, _, _ string, _, out any) error {
			*out.(*rex.Size) = rex.Size{Columns: 80, Rows: 24}
			return nil
		}),
		Prepare: func(ctx context.Context, _ Source) (rex.Size, RenderFunc, error) {
			timing.Record(ctx, "process_done", time.Now(), "ok", true)
			return rex.Size{Columns: 80, Rows: 24}, func(context.Context, Source) (string, error) { return "line", nil }, nil
		},
	}
	trace := timing.New("event", time.Now())
	result := m.renderFrame(timing.WithTrace(context.Background(), trace), Source{WindowID: "window:test", Rect: rex.Rect{0, 0, 1, 1}})
	if result.err != nil {
		t.Fatal(result.err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var record struct{ Window string }
		if err := json.Unmarshal([]byte(row), &record); err != nil {
			t.Fatal(err)
		}
		if record.Window != "window:test" {
			t.Fatalf("uncorrelated window: %s", row)
		}
	}
}
