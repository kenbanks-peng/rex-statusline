package render

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeWatchConfig(t *testing.T, path, executable, text string) {
	t.Helper()
	data := fmt.Sprintf("[renderer]\nexecutable = %q\ntimeout = '1s'\n", executable)
	writeWatchTheme(t, path, text)
	// Each save replaces the inode, as editors commonly do.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
}

func writeWatchTheme(t *testing.T, path, text string) {
	t.Helper()
	path = filepath.Join(filepath.Dir(path), "oh-my-posh.toml")
	data := fmt.Sprintf("version = 4\ntext = %q\n", text)
	if err := os.WriteFile(path+".tmp", []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".tmp", path); err != nil {
		t.Fatal(err)
	}
}

func TestReloadConfig(t *testing.T) {
	ctx := context.Background()
	executable := fakeOMP(t, "omp")
	path := filepath.Join(t.TempDir(), "config.toml")
	writeWatchConfig(t, path, executable, "first")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	r, err := New(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	firstDir := r.dir
	if err := r.reload(ctx, path); err != nil || r.dir != firstDir {
		t.Fatalf("unchanged config rebuilt: %v", err)
	}
	writeWatchTheme(t, path, "second")
	if err := r.reload(ctx, path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(firstDir); !os.IsNotExist(err) {
		t.Fatalf("old theme not removed: %v", err)
	}
	secondDir := r.dir
	for _, theme := range []string{r.theme, r.unknownTheme} {
		data, err := os.ReadFile(theme)
		if err != nil {
			t.Fatal(err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(data, &decoded); err != nil || decoded["text"] != "second" {
			t.Fatalf("theme not updated: %s, %v", data, err)
		}
	}
	for _, invalid := range []string{"[broken", "[renderer]\ntimeout = '-1s'", "[window]\nwidth = '101%'"} {
		if err := os.WriteFile(path, []byte(invalid), 0600); err != nil {
			t.Fatal(err)
		}
		if err := r.reload(ctx, path); err == nil || r.dir != secondDir {
			t.Fatalf("invalid config replaced renderer: %v", err)
		}
	}
	writeWatchConfig(t, path, filepath.Join(t.TempDir(), "missing-omp"), "bad executable")
	if err := r.reload(ctx, path); err == nil || r.dir != secondDir {
		t.Fatalf("unusable executable replaced renderer: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := r.reload(ctx, path); err == nil || r.dir != secondDir {
		t.Fatalf("missing explicit config replaced renderer: %v", err)
	}
	writeWatchConfig(t, path, executable, "recovered")
	if err := r.reload(ctx, path); err != nil || r.dir == secondDir {
		t.Fatalf("did not recover: %v", err)
	}
}

func TestReloadConfiguredTheme(t *testing.T) {
	ctx := context.Background()
	executable := fakeOMP(t, "omp")
	path := filepath.Join(t.TempDir(), "config.toml")
	writeWatchConfig(t, path, executable, "first")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	r, err := New(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	custom := filepath.Join(filepath.Dir(path), "custom.toml")
	if err := os.WriteFile(custom, []byte("version = 4\ntext = 'selected'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	data := fmt.Sprintf("[renderer]\nexecutable = %q\ntheme = 'custom.toml'\n", executable)
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	if err := r.reload(ctx, path); err != nil || r.cfg.Theme["text"] != "selected" {
		t.Fatalf("theme selection not reloaded: %v", err)
	}
	if err := os.WriteFile(custom, []byte("version = 4\ntext = 'updated'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := r.reload(ctx, path); err != nil || r.cfg.Theme["text"] != "updated" {
		t.Fatalf("custom theme edit not reloaded: %v", err)
	}
}

func TestWatchSymlinkAndCancellation(t *testing.T) {
	executable := fakeOMP(t, "omp")
	dir := t.TempDir()
	target := filepath.Join(dir, "target.toml")
	path := filepath.Join(dir, "config.toml")
	writeWatchConfig(t, target, executable, "first")
	if err := os.Symlink(target, path); err != nil {
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
	writeWatchConfig(t, target, executable, "updated")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	failures := make(chan error, 10)
	go func() {
		defer close(done)
		r.Watch(ctx, path, func(err error) { failures <- err })
	}()
	deadline := time.After(5 * time.Second)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		r.mu.RLock()
		updated := r.cfg.Theme["text"] == "updated"
		r.mu.RUnlock()
		if updated {
			break
		}
		select {
		case err := <-failures:
			t.Fatal(err)
		case <-deadline:
			t.Fatal("watch did not reload symlink target")
		case <-tick.C:
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("watch did not stop")
	}
}
