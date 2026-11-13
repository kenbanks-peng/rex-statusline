package render

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"rex-statusline/internal/timing"
)

func TestRenderCacheTracksFreshDependenciesOnPoll(t *testing.T) {
	cfg := fakeConfig(t)
	cfg.Theme["blocks"] = []any{map[string]any{"segments": []any{map[string]any{
		"type": "text", "template": "{{ .Env.POSH_HOST_LABEL }}{{ .Env.PYTHON_SEGMENT }}{{ .Env.JS_SEGMENT }}{{ .Env.GITSTATUS }}",
	}}}}
	r, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	path := filepath.Join(t.TempDir(), "timing.jsonl")
	logger, err := timing.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()
	ctxFor := func(trigger, window string) context.Context {
		trace := timing.New(trigger, time.Now())
		trace.Window = window
		return timing.WithLogger(timing.WithTrace(context.Background(), trace), logger)
	}
	input := Input{ShellKnown: true, Cwd: t.TempDir(), Columns: 200, Env: map[string]string{"SECRET_RENDER_CONTEXT": "DO_NOT_LOG_THIS"}}
	render := func(trigger, window string) {
		t.Helper()
		if _, err := r.Render(ctxFor(trigger, window), input); err != nil {
			t.Fatal(err)
		}
	}
	render("startup", "window:a")
	render("event", "window:a") // Identical inputs reuse the completed render.
	uid := 42
	for _, change := range []func(){
		func() { input.UserID = &uid },
		func() { input.Cwd = t.TempDir() },
		func() { input.Jobs++ },
		func() { input.ExitCode++ },
		func() { input.DurationMS++ },
		func() { input.Columns++ },
		func() { input.Env["UPDATED"] = "new environment" },
		func() { input.ShellKnown = false },
	} {
		change()
		render("event", "window:a")
	}
	render("event", "window:b") // Caches are window-local.
	render("poll", "window:a")  // Fresh native values permit OMP reuse.

	render("event", "window:a")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "DO_NOT_LOG_THIS") || strings.Contains(string(data), input.Cwd) {
		t.Fatal("private render inputs leaked into timing records")
	}
	hits, prints := 0, 0
	segments := map[string]bool{}
	for _, row := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var record struct {
			Msg, Window, Tool, Segment string
			DurationMS                 float64 `json:"duration_ms"`
		}
		if err := json.Unmarshal([]byte(row), &record); err != nil {
			t.Fatal(err)
		}
		if record.Window == "" {
			t.Fatal("uncorrelated renderer timing")
		}
		switch record.Msg {
		case "render_cache_hit":
			hits++
		case "subprocess_done":
			if record.Tool == filepath.Base(cfg.Executable) {
				prints++
			}
		case "segment_done":
			segments[record.Segment] = true
		}
	}
	if hits != 3 || prints != 10 {
		t.Fatalf("hits %d prints %d, want 3 and 10", hits, prints)
	}
	for _, name := range []string{"host", "python", "javascript", "git"} {
		if !segments[name] {
			t.Fatalf("missing %s segment timing", name)
		}
	}
}

func TestRenderCacheInvalidatedByConfigurationReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	executable := fakeOMP(t, "omp")
	writeWatchConfig(t, path, executable, "first")
	themePath := filepath.Join(filepath.Dir(path), "oh-my-posh.toml")
	if err := os.WriteFile(themePath, []byte("version = 4\n[[blocks]]\n[[blocks.segments]]\ntype = 'text'\ntemplate = 'first'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	r, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	trace := timing.New("event", time.Now())
	trace.Window = "window:a"
	ctx := timing.WithTrace(context.Background(), trace)
	input := Input{Cwd: t.TempDir(), Columns: 80}
	if _, err := r.Render(ctx, input); err != nil {
		t.Fatal(err)
	}
	key := r.cache["window:a"].key
	if _, ok := r.cached(ctx, key); !ok {
		t.Fatal("render not cached")
	}
	writeWatchTheme(t, path, "changed")
	if err := r.reload(ctx, path); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.cached(ctx, key); ok {
		t.Fatal("theme reload retained stale render")
	}
	if r.cacheable || r.dependencies != allNative {
		t.Fatal("theme reload retained stale dependency policy")
	}
}

func TestCanceledRenderCannotUseCachedSuccess(t *testing.T) {
	r, err := New(context.Background(), fakeConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	trace := timing.New("event", time.Now())
	trace.Window = "window:a"
	ctx := timing.WithTrace(context.Background(), trace)
	input := Input{Cwd: t.TempDir(), Columns: 80}
	if _, err := r.Render(ctx, input); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := r.Render(ctx, input); err == nil {
		t.Fatal("canceled render returned cached success")
	}
}
