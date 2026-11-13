package render

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestUnknownShellState(t *testing.T) {
	cfg := fakeConfig(t)
	cfg.Theme = map[string]any{"version": 4, "blocks": []any{map[string]any{"type": "prompt", "segments": []any{
		map[string]any{"type": "status", "options": map[string]any{"always_enabled": true}},
		map[string]any{"type": "executiontime"}, map[string]any{"type": "jobs"}, map[string]any{"type": "path"},
	}}}}
	r, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	data, err := os.ReadFile(r.unknownTheme)
	if err != nil {
		t.Fatal(err)
	}
	var theme struct {
		Blocks []struct{ Segments []struct{ Type string } }
	}
	if err = json.Unmarshal(data, &theme); err != nil {
		t.Fatal(err)
	}
	segments := theme.Blocks[0].Segments
	if len(segments) != 1 || segments[0].Type != "path" {
		t.Fatalf("unknown state has shell segments: %s", data)
	}
	out, err := r.Render(context.Background(), Input{Cwd: t.TempDir(), Jobs: 3, ExitCode: 7, DurationMS: 9999, Columns: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "--status") || strings.Contains(out, "--execution-time") || strings.Contains(out, " 3") || !strings.Contains(out, "theme-unknown.json") {
		t.Fatal("fabricated shell values", out)
	}
}
