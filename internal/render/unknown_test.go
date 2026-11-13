package render

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestDefaultThemeEndcap(t *testing.T) {
	if _, err := exec.LookPath("oh-my-posh"); err != nil {
		t.Skip("Oh My Posh is not installed")
	}
	cfg, err := Load("../defaults/config.toml")
	if err != nil {
		t.Fatal(err)
	}
	r, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	cwd := t.TempDir()
	var unknown string
	for _, tc := range []struct {
		name  string
		known bool
		code  int
	}{
		{"success", true, 0},
		{"failure", true, 7},
		{"unknown", false, 0},
		{"unknown_ignores_shell_values", false, 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			line, err := r.Render(context.Background(), Input{
				Cwd: cwd, Columns: 200, ShellKnown: tc.known, ExitCode: tc.code,
				Jobs: 3, DurationMS: 9999,
			})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasSuffix(ansi.Strip(line), "") {
				t.Fatalf("missing endcap: %q", line)
			}
			// The default theme uses #a90000 only for root and failed status.
			if got := strings.Contains(line, "169;0;0"); got != (tc.known && tc.code > 0) {
				t.Fatalf("incorrect failure color: %q", line)
			}
			if !tc.known {
				if unknown != "" && line != unknown {
					t.Fatalf("unknown status depends on shell values: %q != %q", line, unknown)
				}
				unknown = line
			}
		})
	}
}

func TestUnknownShellState(t *testing.T) {
	cfg := fakeConfig(t)
	cfg.Theme = map[string]any{"version": 4, "blocks": []any{map[string]any{"type": "prompt", "segments": []any{
		map[string]any{
			"type": "status", "style": "accordion", "powerline_symbol": "",
			"background": "blue", "foreground": "white",
			"template": "{{ .Meaning }}", "background_templates": []any{"{{ if .Error }}red{{ end }}"},
			"options": map[string]any{"always_enabled": true},
		},
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
		Blocks []struct{ Segments []map[string]any }
	}
	if err = json.Unmarshal(data, &theme); err != nil {
		t.Fatal(err)
	}
	segments := theme.Blocks[0].Segments
	want := []map[string]any{
		{"type": "text", "style": "accordion", "powerline_symbol": "", "background": "blue", "foreground": "white", "template": "\u00a0  "},
		{"type": "path"},
	}
	if !reflect.DeepEqual(segments, want) {
		t.Fatalf("unknown state lost decoration or retained shell state: %s", data)
	}
	out, err := r.Render(context.Background(), Input{Cwd: t.TempDir(), Jobs: 3, ExitCode: 7, DurationMS: 9999, Columns: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "--status") || strings.Contains(out, "--execution-time") || strings.Contains(out, " 3") || !strings.Contains(out, "theme-unknown.json") {
		t.Fatal("fabricated shell values", out)
	}
}
