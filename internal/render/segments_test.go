package render

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T, dir, name, contents string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
}
func TestDirectoryPaths(t *testing.T) {
	home := "/home/ken"
	for path, want := range map[string]string{
		home: " HOME", home + "/.local": " LOCAL", home + "/.cache": " CACHE", home + "/.config": "󰒓 CONFIG",
		home + "/.config/rex": "󰒓/rex", "file://localhost/home/ken//.config/rex/": "󰒓/rex",
		home + "/世界/e\u0301": "/世界/e\u0301", "/home/kenn": " /home/kenn",
		"/ext/abcdefghijklmnopqrstuvw": "abcdefghijklmnopq...",
	} {
		if got := directoryPath(path, home); got != want {
			t.Errorf("%q: %q want %q", path, got, want)
		}
	}
	if got := normalizePath("file://remote/tmp/a"); got != "file://remote/tmp/a" {
		t.Fatal("accepted remote URL")
	}
	if got := normalizePath("file:///tmp/a%20b"); got != "/tmp/a b" {
		t.Fatal(got)
	}
}
func TestNativeLanguageSegments(t *testing.T) {
	r := &Renderer{}
	dir := t.TempDir()
	bin := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"node", "deno", "python3"} {
		if err := os.WriteFile(filepath.Join(bin, name), data, 0700); err != nil {
			t.Fatal(err)
		}
	}
	env := baseEnv(map[string]string{"PATH": bin})
	in := Input{Cwd: dir, Jobs: 2}
	fixture(t, dir, "main.ts", "")
	got := nativeSegmentsFor(context.Background(), r, in, env, allNative)
	if !strings.Contains(got["JS_SEGMENT"], "") || got["PYTHON_SEGMENT"] != "" {
		t.Fatalf("%v", got)
	}
	fixture(t, dir, "main.py", "")
	got = nativeSegmentsFor(context.Background(), r, in, env, allNative)
	if got["PYTHON_SEGMENT"] != "󱔎" {
		t.Fatal(got)
	}
	fixture(t, dir, "pyproject.toml", "")
	fixture(t, dir, "package.json", `{"dependencies":{}}`)
	fixture(t, dir, "bun.lock", "")
	got = nativeSegmentsFor(context.Background(), r, in, env, allNative)
	if got["PYTHON_SEGMENT"] != "󱔎 3.14.0" || !strings.Contains(got["JS_SEGMENT"], " 󰆧 24.1.0") {
		t.Fatal(got)
	}
	fixture(t, dir, "package.json", `{"dependencies":{"react-dom":"^19.0.0"}}`)
	fixture(t, dir, "node_modules/react-dom/package.json", `{"version":"19.2.0"}`)
	got = nativeSegmentsFor(context.Background(), r, in, env, allNative)
	if !strings.Contains(got["JS_SEGMENT"], " 19.2.0") {
		t.Fatal(got)
	}
	fixture(t, dir, "svelte.config.js", "")
	fixture(t, dir, "node_modules/svelte/package.json", `{"version":"5.0.1"}`)
	got = nativeSegmentsFor(context.Background(), r, in, env, allNative)
	if got["JS_SEGMENT"] != "  5.0.1" {
		t.Fatal(got)
	}
	fixture(t, dir, "deno.json", "")
	got = nativeSegmentsFor(context.Background(), r, in, env, allNative)
	if got["JS_SEGMENT"] != "  2.5.0" {
		t.Fatal(got)
	}
	// Ancestor detection and target virtualenv interpreter.
	venv := t.TempDir()
	os.MkdirAll(filepath.Join(venv, "bin"), 0700)
	os.WriteFile(filepath.Join(venv, "bin/python"), data, 0700)
	env["VIRTUAL_ENV"] = venv
	in.Cwd = filepath.Join(dir, "nested")
	os.Mkdir(in.Cwd, 0700)
	got = nativeSegmentsFor(context.Background(), r, in, env, allNative)
	if got["PYTHON_SEGMENT"] != " 3.14.0" {
		t.Fatal(got)
	}
}
func TestNativeGit(t *testing.T) {
	executable, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git unavailable")
	}
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(executable, args...)
		cmd.Dir = dir
		cmd.Env = []string{"PATH=" + filepath.Dir(executable), "HOME=" + t.TempDir(), "GIT_CONFIG_NOSYSTEM=1"}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s %v", args, out, err)
		}
		return strings.TrimSpace(string(out))
	}
	branch := "long-branch-name-over-twenty-five-characters"
	git("init", "-b", branch)
	git("config", "user.name", "Fixture")
	git("config", "user.email", "fixture@example.invalid")
	fixture(t, dir, "a", "1")
	git("add", ".")
	git("commit", "-m", "first")
	first := git("rev-parse", "HEAD")
	fixture(t, dir, "a", "2")
	git("commit", "-am", "second")
	git("remote", "add", "origin", "git@github.com:fixture/project.git")
	git("remote", "add", "upstream", "https://gitlab.com/fixture/project")
	git("update-ref", "refs/remotes/origin/"+branch, first)
	git("config", "branch."+branch+".remote", "origin")
	git("config", "branch."+branch+".merge", "refs/heads/"+branch)
	tree := git("rev-parse", "HEAD^{tree}")
	other := git("commit-tree", tree, "-p", first, "-m", "other")
	git("update-ref", "refs/remotes/upstream/"+branch, other)
	fixture(t, dir, "dirty", "untracked")
	env := baseEnv(map[string]string{"PATH": filepath.Dir(executable), "HOME": t.TempDir(), "GIT_CONFIG_NOSYSTEM": "1"})
	values := map[string]string{"GITSTATUS_CHANGED": "0", "GITSTATUS_AHEAD": "0", "GITSTATUS_BEHIND": "0"}
	gitSegments(context.Background(), &Renderer{}, dir, env, values)
	if values["GITSTATUS_CHANGED"] != "1" || values["GITSTATUS_AHEAD"] != "2" || values["GITSTATUS_BEHIND"] != "1" {
		t.Fatalf("%v", values)
	}
	for _, want := range []string{"", "…", "", "1", "1", "󱩽"} {
		if !strings.Contains(values["GITSTATUS"], want) {
			t.Fatalf("missing %q: %v", want, values)
		}
	}
	git("checkout", "--detach")
	gitSegments(context.Background(), &Renderer{}, dir, env, values)
	if !strings.Contains(values["GITSTATUS"], ""+git("rev-parse", "--short", "HEAD")) {
		t.Fatal(values)
	}
}
