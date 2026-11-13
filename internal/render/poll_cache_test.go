package render

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"rex-statusline/internal/timing"
)

// This fake OMP has exactly the dependencies declared by the test theme.
func pollingRenderer(t *testing.T, source string) (*Renderer, Input, func() int) {
	t.Helper()
	theme := map[string]any{
		"version": 4, "blocks": []any{map[string]any{
			"type": "prompt", "segments": []any{map[string]any{"type": "text", "template": source}},
		}},
	}
	return pollingThemeRenderer(t, theme)
}

func pollingThemeRenderer(t *testing.T, theme map[string]any) (*Renderer, Input, func() int) {
	t.Helper()
	dir := t.TempDir()
	executable := filepath.Join(dir, "omp")
	calls := filepath.Join(dir, "calls")
	script := `#!/bin/sh
if [ "$1" = version ]; then printf '32.0.0'; exit; fi
printf 'print\n' >> "$OMP_CALLS"
printf '%s|%s|%s' "$GITSTATUS" "$JS_SEGMENT" "$PYTHON_SEGMENT"
if [ -n "$OMP_STATE_FILE" ]; then /bin/cat "$OMP_STATE_FILE"; fi
`
	if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Executable: executable, Theme: theme}
	r, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	input := Input{ShellKnown: true, Cwd: t.TempDir(), Columns: 200,
		Env: map[string]string{"OMP_CALLS": calls}}
	count := func() int {
		t.Helper()
		data, err := os.ReadFile(calls)
		if err != nil {
			t.Fatal(err)
		}
		return strings.Count(string(data), "print\n")
	}
	return r, input, count
}

func pollRender(t *testing.T, r *Renderer, input Input) string {
	t.Helper()
	trace := timing.New("poll", time.Now())
	trace.Window = "window:a"
	line, err := r.Render(timing.WithTrace(context.Background(), trace), input)
	if err != nil {
		t.Fatal(err)
	}
	return stripSGR(line)
}

func TestPollReusesOMPOnlyAfterFreshGitDependencies(t *testing.T) {
	r, input, count := pollingRenderer(t, "{{ .Env.GITSTATUS }}")
	f := newGitFixture(t)
	input.Cwd = f.dir
	for key, value := range f.env {
		input.Env[key] = value
	}
	first := pollRender(t, r, input)
	if again := pollRender(t, r, input); again != first || count() != 1 {
		t.Fatalf("unchanged poll reran OMP: first %q again %q prints %d", first, again, count())
	}
	if calls, err := os.ReadFile(f.calls); err != nil || strings.Count(string(calls), "call\n") != 4 {
		t.Fatalf("unchanged poll must still refresh Git status and config: %s, %v", calls, err)
	}
	// Neither the input nor .git changes when another process edits a worktree file.
	fixture(t, f.dir, "external", "new")
	dirty := pollRender(t, r, input)
	if dirty == first || count() != 2 {
		t.Fatalf("external edit was hidden: %q -> %q, prints %d", first, dirty, count())
	}
	if err := os.Remove(filepath.Join(f.dir, "external")); err != nil {
		t.Fatal(err)
	}
	if clean := pollRender(t, r, input); clean != first || count() != 3 {
		t.Fatalf("external cleanup was hidden: %q, prints %d", clean, count())
	}
	f.git("symbolic-ref", "HEAD", "refs/heads/other")
	if branch := pollRender(t, r, input); !strings.Contains(branch, "other") || count() != 4 {
		t.Fatalf("branch change was hidden: %q, prints %d", branch, count())
	}
	// A remote config change affects the service icon without changing status.
	f.git("remote", "add", "origin", "https://github.com/example/project")
	if remote := pollRender(t, r, input); !strings.Contains(remote, "") || count() != 5 {
		t.Fatalf("Git config change was hidden: %q, prints %d", remote, count())
	}
}

func TestPollRechecksNativeLanguageFiles(t *testing.T) {
	r, input, count := pollingRenderer(t, "{{ .Env.JS_SEGMENT }}{{ .Env.PYTHON_SEGMENT }}")
	first := pollRender(t, r, input)
	pollRender(t, r, input)
	if count() != 1 {
		t.Fatalf("unchanged poll prints %d", count())
	}
	fixture(t, input.Cwd, "external.ts", "")
	js := pollRender(t, r, input)
	if js == first || count() != 2 {
		t.Fatalf("new TypeScript file hidden: %q, prints %d", js, count())
	}
	fixture(t, input.Cwd, "external.py", "")
	py := pollRender(t, r, input)
	if py == js || count() != 3 {
		t.Fatalf("new Python file hidden: %q, prints %d", py, count())
	}
}

func TestInputOnlyThemeSkipsUnreferencedNativeProbes(t *testing.T) {
	r, input, count := pollingRenderer(t, "static")
	f := newGitFixture(t)
	input.Cwd = f.dir
	for key, value := range f.env {
		input.Env[key] = value
	}
	pollRender(t, r, input)
	pollRender(t, r, input)
	if count() != 1 {
		t.Fatalf("unchanged static theme prints %d", count())
	}
	if data, err := os.ReadFile(f.calls); err == nil && len(data) != 0 {
		t.Fatalf("theme without Git dependencies ran Git: %s", data)
	} else if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestAuditedBuiltinsSkipUnusedNativeProbesButStillEvaluateOMP(t *testing.T) {
	for _, kind := range []string{"os", "go", "lua", "zig", "docker"} {
		t.Run(kind, func(t *testing.T) {
			segment := map[string]any{"type": kind}
			if kind == "docker" {
				segment["options"] = map[string]any{"display_mode": "files"}
			}
			theme := map[string]any{"version": 4, "blocks": []any{map[string]any{
				"type": "prompt", "segments": []any{segment},
			}}}
			r, input, count := pollingThemeRenderer(t, theme)
			if r.dependencies != 0 || r.cacheable {
				t.Fatalf("built-in policy: deps %v cacheable %v", r.dependencies, r.cacheable)
			}
			f := newGitFixture(t)
			input.Cwd = f.dir
			for key, value := range f.env {
				input.Env[key] = value
			}
			// The fake models an external built-in dependency, outside the cwd.
			state := filepath.Join(t.TempDir(), "state")
			input.Env["OMP_STATE_FILE"] = state
			fixture(t, filepath.Dir(state), "state", "first")
			first := pollRender(t, r, input)
			pollRender(t, r, input)
			fixture(t, filepath.Dir(state), "state", "changed")
			changed := pollRender(t, r, input)
			if first == changed || count() != 3 {
				t.Fatalf("built-in output cached: %q -> %q, prints %d", first, changed, count())
			}
			if data, err := os.ReadFile(f.calls); err == nil && len(data) != 0 {
				t.Fatalf("unreferenced Git ran: %s", data)
			} else if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
		})
	}
}

func TestBundledThemePollPreservesOMPFileActivation(t *testing.T) {
	if _, err := exec.LookPath("oh-my-posh"); err != nil {
		t.Skip("OMP not installed")
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
	input := Input{ShellKnown: true, Cwd: t.TempDir(), Columns: 400,
		Env: map[string]string{"HOME": t.TempDir()}}
	before := pollRender(t, r, input)
	if strings.Contains(before, "") {
		t.Fatalf("Docker segment unexpectedly active: %q", before)
	}
	fixture(t, input.Cwd, "Dockerfile", "FROM scratch\n")
	after := pollRender(t, r, input)
	if !strings.Contains(after, "") {
		t.Fatalf("external Dockerfile creation hidden: %q", after)
	}
	if err := os.Remove(filepath.Join(input.Cwd, "Dockerfile")); err != nil {
		t.Fatal(err)
	}
	if removed := pollRender(t, r, input); strings.Contains(removed, "") {
		t.Fatalf("external Dockerfile removal hidden: %q", removed)
	}
}
