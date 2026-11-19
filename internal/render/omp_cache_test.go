package render

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func TestOMPCacheEnvironment(t *testing.T) {
	shared := t.TempDir()
	t.Setenv("OMP_CACHE_DIR", shared)
	t.Setenv("POSH_SESSION_ID", "manager-session")
	ctx := context.Background()
	cfg := fakeConfig(t)
	r, err := New(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	input := Input{Cwd: t.TempDir(), Columns: 4096, Env: map[string]string{
		"FAKE_MODE": "omp-env", "OMP_CACHE_DIR": shared, "POSH_SESSION_ID": "shell-session",
	}}
	check := func() {
		t.Helper()
		for _, known := range []bool{true, false} {
			input.ShellKnown = known
			out, err := r.Render(ctx, input)
			if err != nil {
				t.Fatal(err)
			}
			var env map[string]string
			if err := json.Unmarshal([]byte(ansi.Strip(out)), &env); err != nil {
				t.Fatalf("environment %q: %v", out, err)
			}
			if env["OMP_CACHE_DIR"] != r.dir || env["POSH_SESSION_ID"] != "rex-statusline" {
				t.Fatalf("OMP environment not isolated: %v", env)
			}
		}
	}
	check()
	check()
	if input.Env["OMP_CACHE_DIR"] != shared || input.Env["POSH_SESSION_ID"] != "shell-session" {
		t.Fatal("input environment mutated")
	}
	// Native tools still receive the authoritative block environment.
	env := baseEnv(input.Env)
	if env["OMP_CACHE_DIR"] != shared || env["POSH_SESSION_ID"] != "shell-session" {
		t.Fatal("native environment changed")
	}
	oldDir := r.dir
	cache := filepath.Join(oldDir, "oh-my-posh")
	if err := os.Mkdir(cache, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "session.cache"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	writeWatchConfig(t, path, cfg.Executable, "reloaded")
	if err := r.reload(ctx, path); err != nil {
		t.Fatal(err)
	}
	if r.dir == oldDir {
		t.Fatal("reload retained old cache root")
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Fatalf("reload left old cache: %v", err)
	}
	check()
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(r.dir); !os.IsNotExist(err) {
		t.Fatalf("close left cache root: %v", err)
	}
	entries, err := os.ReadDir(shared)
	if err != nil || len(entries) != 0 {
		t.Fatalf("shared cache touched: %v, %v", entries, err)
	}
}

func TestRepeatedRendersWithInstalledOMPBoundCache(t *testing.T) {
	if _, err := exec.LookPath("oh-my-posh"); err != nil {
		t.Skip("OMP not installed")
	}
	var theme map[string]any
	if err := json.Unmarshal([]byte(`{"version":4,"blocks":[{"type":"prompt","alignment":"left","segments":[{"type":"text","style":"plain","template":"test"}]}]}`), &theme); err != nil {
		t.Fatal(err)
	}
	shared := t.TempDir()
	t.Setenv("OMP_CACHE_DIR", shared)
	t.Setenv("XDG_CACHE_HOME", shared)
	ctx := context.Background()
	r, err := New(ctx, Config{Theme: theme, Timeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	in := Input{Cwd: t.TempDir(), Columns: 80, Env: map[string]string{
		"OMP_CACHE_DIR": shared, "XDG_CACHE_HOME": shared, "POSH_SESSION_ID": "shell-session",
	}}
	var first []string
	for i := 0; i < 20; i++ {
		in.ShellKnown = i%2 == 0
		if _, err := r.Render(ctx, in); err != nil {
			t.Fatal(err)
		}
		entries, err := os.ReadDir(filepath.Join(r.dir, "oh-my-posh"))
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		if i == 0 {
			first = names
			if len(first) == 0 {
				t.Fatal("OMP did not exercise its cache")
			}
		} else if !reflect.DeepEqual(first, names) {
			t.Fatalf("cache grew: %v -> %v", first, names)
		}
	}
	entries, err := os.ReadDir(shared)
	if err != nil || len(entries) != 0 {
		t.Fatalf("shared cache touched: %v, %v", entries, err)
	}
	dir := r.dir
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("OMP cache survived close: %v", err)
	}
}
