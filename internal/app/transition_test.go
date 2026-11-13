package app

import (
	"context"
	"github.com/charmbracelet/x/ansi"
	"os"
	"path/filepath"
	"testing"
	"time"

	"rex-statusline/internal/bsp"
	"rex-statusline/internal/manager"
	"rex-statusline/internal/render"
	"rex-statusline/internal/rex"
)

func TestDirectoryTransitionWaitsForShellContext(t *testing.T) {
	ctx := context.Background()
	old, next := t.TempDir(), t.TempDir()
	executable := filepath.Join(t.TempDir(), "omp")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nif [ \"$1\" = version ]; then echo 32.0.0; else printf '%s/%s' \"$BSP_SHELL_CONTEXT\" \"$VIRTUAL_ENV\"; fi\n"), 0700); err != nil {
		t.Fatal(err)
	}
	renderer, err := render.New(ctx, render.Config{Executable: executable, Theme: map[string]any{"version": 4}})
	if err != nil {
		t.Fatal(err)
	}
	defer renderer.Close()
	pid := os.Getpid()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	source := manager.Source{SessionID: "session:s", Block: rex.Block{ID: "block:b", HostID: "host"}, Size: rex.Size{Columns: 80}}
	store := bsp.Store{Root: root, ServerID: "instance", SessionID: source.SessionID}
	publish := func(cwd, env string) error {
		return store.Publish(source.Block.ID, pid, "local", bsp.Report{Kind: "context", Context: bsp.Context{Cwd: cwd, Env: map[string]string{"VIRTUAL_ENV": env}}})
	}
	if err := publish(old, "OLD"); err != nil {
		t.Fatal(err)
	}
	resolver := Resolver{API: fakeAPI{rex.Process{Foreground: rex.ProcessEntry{PID: pid, Name: "zsh", Cwd: next}}}, Identity: rex.Identity{HostID: "host", InstanceID: "instance"}, Root: root, Renderer: renderer}
	done := make(chan error, 1)
	go func() { time.Sleep(25 * time.Millisecond); done <- publish(next, "NEW") }()
	line, err := resolver.Render(ctx, source)
	if publishErr := <-done; publishErr != nil {
		t.Fatal(publishErr)
	}
	if err != nil || ansi.Strip(line) != "known/NEW" {
		t.Fatalf("intermediate fallback frame: %q, %v", line, err)
	}
	// If publication never catches up, do not reuse the old environment.
	if err := publish(old, "OLD"); err != nil {
		t.Fatal(err)
	}
	line, err = resolver.Render(ctx, source)
	if err != nil || ansi.Strip(line) != "unknown/" {
		t.Fatalf("stale environment: %q, %v", line, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := resolver.Render(cancelled, source); err != context.Canceled {
		t.Fatalf("cancellation: %v", err)
	}
}
