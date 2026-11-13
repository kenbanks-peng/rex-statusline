package render

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type gitFixture struct {
	t                      *testing.T
	dir, executable, calls string
	env                    map[string]string
}

func newGitFixture(t *testing.T) *gitFixture {
	t.Helper()
	executable, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git unavailable")
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		t.Fatal(err)
	}
	f := &gitFixture{t: t, dir: t.TempDir(), executable: executable, calls: filepath.Join(t.TempDir(), "calls")}
	bin := t.TempDir()
	script := "#!/bin/sh\nprintf 'call\\n' >> \"$CALLS\"\nexec \"$REAL_GIT\" \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	f.env = baseEnv(map[string]string{"PATH": bin, "HOME": t.TempDir(), "GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": os.DevNull, "REAL_GIT": executable, "CALLS": f.calls})
	f.git("init", "-b", "main")
	f.git("config", "user.name", "Fixture")
	f.git("config", "user.email", "fixture@example.invalid")
	return f
}

func (f *gitFixture) git(args ...string) string {
	f.t.Helper()
	cmd := exec.Command(f.executable, args...)
	cmd.Dir = f.dir
	for k, v := range f.env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("git %v: %s: %v", args, out, err)
	}
	return strings.TrimSpace(string(out))
}

func (f *gitFixture) render(wantCalls int) map[string]string {
	f.t.Helper()
	if err := os.WriteFile(f.calls, nil, 0600); err != nil {
		f.t.Fatal(err)
	}
	values := map[string]string{"GITSTATUS_CHANGED": "0", "GITSTATUS_AHEAD": "0", "GITSTATUS_BEHIND": "0"}
	gitSegments(context.Background(), &Renderer{}, f.dir, f.env, values)
	data, err := os.ReadFile(f.calls)
	if err != nil {
		f.t.Fatal(err)
	}
	if got := strings.Count(string(data), "call\n"); got != wantCalls {
		f.t.Fatalf("git calls %d want %d: %v", got, wantCalls, values)
	}
	return values
}

func TestGitConsolidatedFreshState(t *testing.T) {
	f := newGitFixture(t)
	if got := f.render(2); !strings.Contains(got["GITSTATUS"], "main") || got["GITSTATUS_CHANGED"] != "0" {
		t.Fatal(got)
	}
	fixture(t, f.dir, "a", "first")
	f.git("add", ".")
	f.git("commit", "-m", "first")
	first := f.git("rev-parse", "HEAD")
	f.git("remote", "add", "origin", "alias:fixture/project")
	f.git("config", "url.https://github.com/.insteadOf", "alias:")
	f.git("update-ref", "refs/remotes/origin/main", first)
	f.git("config", "branch.main.remote", "origin")
	f.git("config", "branch.main.merge", "refs/heads/main")
	f.git("config", "status.aheadBehind", "false")
	if got := f.render(2); !strings.Contains(got["GITSTATUS"], "") || got["GITSTATUS_CHANGED"] != "0" {
		t.Fatal(got)
	}
	// Configuration and URL rewrites are never cached.
	f.git("config", "--unset", "url.https://github.com/.insteadOf")
	f.git("config", "url.https://gitlab.com/.insteadOf", "alias:")
	if got := f.render(2); !strings.Contains(got["GITSTATUS"], "") {
		t.Fatal(got)
	}
	fixture(t, f.dir, "a", "second")
	if got := f.render(2); got["GITSTATUS_CHANGED"] != "1" {
		t.Fatal(got)
	}
	f.git("stash", "push", "-m", "saved")
	if got := f.render(2); got["GITSTATUS_CHANGED"] != "0" {
		t.Fatalf("stash must not mark worktree dirty: %v", got)
	}
	fixture(t, f.dir, "a", "second")
	f.git("commit", "-am", "second")
	if got := f.render(2); got["GITSTATUS_AHEAD"] != "1" || got["GITSTATUS_BEHIND"] != "0" {
		t.Fatal(got)
	}
	// A second, distinct remote retains the existing comparison semantics.
	f.git("remote", "add", "upstream", "https://example.invalid/project")
	f.git("update-ref", "refs/remotes/upstream/main", first)
	if got := f.render(3); got["GITSTATUS_AHEAD"] != "2" || !strings.Contains(got["GITSTATUS"], "") {
		t.Fatal(got)
	}
	f.git("config", "branch.main.remote", "upstream")
	if got := f.render(2); got["GITSTATUS_AHEAD"] != "1" || strings.Contains(got["GITSTATUS"], "") {
		t.Fatal(got)
	}
	f.git("config", "--unset", "branch.main.remote")
	f.git("config", "--unset", "branch.main.merge")
	// No tracking branch: retain origin/<branch> fallback.
	if got := f.render(4); got["GITSTATUS_AHEAD"] != "2" {
		t.Fatal(got)
	}
	f.git("remote", "remove", "upstream")
	f.git("config", "core.abbrev", "10")
	f.git("checkout", "--detach")
	if got := f.render(4); !strings.Contains(got["GITSTATUS"], ""+f.git("rev-parse", "--short", "HEAD")) {
		t.Fatal(got)
	}
}

func TestGitContextOverridesAndDirtyPaths(t *testing.T) {
	f := newGitFixture(t)
	fixture(t, f.dir, "old", "first")
	f.git("add", ".")
	f.git("commit", "-m", "first")
	f.git("mv", "old", "new\n# branch.ab +100 -100")
	if got := f.render(2); got["GITSTATUS_CHANGED"] != "1" || got["GITSTATUS_AHEAD"] != "0" {
		t.Fatal(got)
	}
	// Explicit Git context must win over the working directory.
	f.env["GIT_DIR"] = filepath.Join(f.dir, ".git")
	f.env["GIT_WORK_TREE"] = f.dir
	f.env["GIT_CONFIG_COUNT"] = "1"
	f.env["GIT_CONFIG_KEY_0"] = "remote.origin.url"
	f.env["GIT_CONFIG_VALUE_0"] = "https://bitbucket.org/fixture/project"
	f.dir = t.TempDir()
	if got := f.render(3); !strings.Contains(got["GITSTATUS"], "") || got["GITSTATUS_CHANGED"] != "1" {
		t.Fatal(got)
	}
}

func TestGitPorcelainRecordsAndURLRewrites(t *testing.T) {
	var writer gitStatusWriter
	_, err := writer.Write([]byte("# branch.head main\x00# branch.upstream upstream/main\x00# branch.ab +2 -3\x002 R. rename\x00# branch.head injected\x00? untracked\x00"))
	if err != nil {
		t.Fatal(err)
	}
	state := writer.state
	if state.branch != "main" || state.upstream != "upstream/main" || !state.dirty || state.ahead != 2 || state.behind != 3 || !state.hasCounts {
		t.Fatalf("%+v", state)
	}
	urls := gitRemoteURLs("remote.origin.url\nshort:project\x00remote.origin.url\nignored\x00url.https://github.com/.insteadof\nshort:\x00url.https://gitlab.com/.insteadof\nshort:pro\x00remote.upstream.url\nuntouched\x00")
	if urls["origin"] != "https://gitlab.com/ject" || urls["upstream"] != "untouched" {
		t.Fatal(urls)
	}
}

func TestGitCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	values := map[string]string{}
	gitSegments(ctx, &Renderer{}, t.TempDir(), baseEnv(nil), values)
	if len(values) != 0 {
		t.Fatal(values)
	}
}
