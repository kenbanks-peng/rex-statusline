package render

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"rex-statusline/internal/timing"
)

func TestPollEvaluatesFilesystemDependentThemeAndUserID(t *testing.T) {
	cfg := fakeConfig(t)
	// A custom command theme can observe files outside the working directory.
	cfg.Theme["blocks"] = []any{map[string]any{"segments": []any{map[string]any{
		"type": "command", "options": map[string]any{"script": "cat external-state"},
	}}}}
	r, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	state := filepath.Join(t.TempDir(), "state")
	uid := 42
	input := Input{Cwd: t.TempDir(), Columns: 80, UserID: &uid,
		Env: map[string]string{"FAKE_MODE": "filesystem", "THEME_STATE_FILE": state}}
	for i, want := range []string{"before|42", "after|42", "event|42"} {
		if err := os.WriteFile(state, []byte(strings.Split(want, "|")[0]), 0600); err != nil {
			t.Fatal(err)
		}
		trigger := "poll"
		if i == 2 {
			trigger = "event"
		}
		trace := timing.New(trigger, time.Now())
		trace.Window = "window:a"
		ctx := timing.WithTrace(context.Background(), trace)
		got, err := r.Render(ctx, input)
		if err != nil || stripSGR(got) != want {
			t.Fatalf("got %q want %q: %v", got, want, err)
		}
	}
	input.UserID = nil
	got, err := r.Render(context.Background(), input)
	if err != nil || stripSGR(got) != "event|unknown" {
		t.Fatalf("unknown UID: %q %v", got, err)
	}
}
