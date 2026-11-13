package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"rex-statusline/internal/bsp"
	"rex-statusline/internal/manager"
	"rex-statusline/internal/render"
	"rex-statusline/internal/rex"
)

type fakeAPI struct{ process rex.Process }

func (f fakeAPI) Call(_ context.Context, _, _ string, _ any, out any) error {
	data, _ := json.Marshal(f.process)
	return json.Unmarshal(data, out)
}
func TestProcessContext(t *testing.T) {
	p := ProcessContext(rex.Process{Foreground: rex.ProcessEntry{PID: 100, Name: "ssh"}, Ancestors: []rex.ProcessEntry{{PID: 10, Name: "zsh"}}}, false)
	if !p.Remote || p.ShellPID != 10 || !p.PIDs[100] {
		t.Fatal(p)
	}
	p = ProcessContext(rex.Process{Foreground: rex.ProcessEntry{PID: 100, Name: "zsh", Script: "/bin/linux"}, Ancestors: []rex.ProcessEntry{{PID: 10, Name: "zsh"}}}, false)
	if !p.Remote || p.ShellPID != 10 {
		t.Fatal(p)
	}
	p = ProcessContext(rex.Process{Foreground: rex.ProcessEntry{PID: 100, Name: "node"}, Ancestors: []rex.ProcessEntry{{PID: 20, Name: "bash"}, {PID: 10, Name: "zsh"}}}, false)
	if p.Remote || p.ShellPID != 20 {
		t.Fatal(p)
	}
}
func TestResolution(t *testing.T) {
	ctx := context.Background()
	cwd := t.TempDir()
	executable := filepath.Join(t.TempDir(), "omp")
	script := "#!/bin/sh\nif [ \"$1\" = version ]; then echo 32.0.0; else printf '%s' \"$BSP_SHELL_CONTEXT/$VIRTUAL_ENV\"; fi\n"
	if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	renderer, err := render.New(ctx, render.Config{Executable: executable, Theme: map[string]any{"version": 4}})
	if err != nil {
		t.Fatal(err)
	}
	defer renderer.Close()
	pid := os.Getpid()
	api := fakeAPI{rex.Process{Foreground: rex.ProcessEntry{PID: pid, Name: "zsh", Cwd: cwd}}}
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	resolver := Resolver{API: api, Identity: rex.Identity{HostID: "host", InstanceID: "instance"}, Root: root, Renderer: renderer}
	s := manager.Source{SessionID: "session:s", Block: rex.Block{ID: "block:b", HostID: "host"}, Size: rex.Size{Columns: 80}}
	t.Setenv("VIRTUAL_ENV", "MANAGER_SECRET")
	line, err := resolver.Render(ctx, s)
	if err != nil || !strings.Contains(line, "unknown/") || strings.Contains(line, "MANAGER_SECRET") {
		t.Fatalf("%q %v", line, err)
	}
	store := bsp.Store{Root: root, ServerID: "instance", SessionID: s.SessionID}
	err = store.Publish(s.Block.ID, pid, "local", bsp.Report{Kind: "context", Context: bsp.Context{Cwd: cwd, Env: map[string]string{"VIRTUAL_ENV": "BLOCK_ENV"}}})
	if err != nil {
		t.Fatal(err)
	}
	line, err = resolver.Render(ctx, s)
	if err != nil || !strings.Contains(line, "known/BLOCK_ENV") {
		t.Fatalf("%q %v", line, err)
	}
	api.process.Foreground.Cwd = t.TempDir()
	resolver.API = api
	line, err = resolver.Render(ctx, s)
	if err != nil || !strings.Contains(line, "unknown/") {
		t.Fatalf("stale directory context: %q %v", line, err)
	}
	api.process.Foreground.Name = "ssh"
	resolver.API = api
	line, err = resolver.Render(ctx, s)
	if err != nil || line != "Remote/container status unavailable" {
		t.Fatalf("%q %v", line, err)
	}
	s.Block.HostID = "remote"
	line, err = resolver.Render(ctx, s)
	if err != nil || line != "Remote Rex host status unavailable" {
		t.Fatalf("%q %v", line, err)
	}
}
func TestNormalize(t *testing.T) {
	for input, want := range map[string]string{"file:///a//b": "/a/b", "file://localhost/a/": "/a", "relative": "", "file://remote/a": "", "file:///a/my%20project": "/a/my project", "file:///a?b": ""} {
		if got := normalize(input); got != want {
			t.Fatalf("%q: %q", input, got)
		}
	}
}
