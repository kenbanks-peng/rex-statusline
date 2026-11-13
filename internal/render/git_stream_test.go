package render

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGitLargeTrackedWorktreeSummary(t *testing.T) {
	f := newGitFixture(t)
	for i := range 900 {
		fixture(t, f.dir, fmt.Sprintf("%05d-%080d", i, i), "before")
	}
	f.git("add", ".")
	f.git("commit", "-m", "many tracked files")
	if got := f.render(2); got["GITSTATUS_CHANGED"] != "0" || !strings.Contains(got["GITSTATUS"], "main") {
		t.Fatal(got)
	}
	for i := range 900 {
		fixture(t, f.dir, fmt.Sprintf("%05d-%080d", i, i), "after")
	}
	if got := f.render(2); got["GITSTATUS_CHANGED"] != "1" || !strings.Contains(got["GITSTATUS"], "main") {
		t.Fatal(got)
	}
}

func TestGitStatusStreamingChunkBoundaries(t *testing.T) {
	status := "# branch.oid abcdef\x00# branch.head main\x00# branch.upstream origin/main\x00" +
		"# branch.ab +2 -3\x002 R. renamed\x00# branch.head injected\x00? untracked\x00"
	for _, size := range []int{1, 2, 3, 7, 4096} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			var out gitStatusWriter
			for i := 0; i < len(status); i += size {
				part := status[i:min(i+size, len(status))]
				if n, err := out.Write([]byte(part)); n != len(part) || err != nil {
					t.Fatalf("Write = %d, %v", n, err)
				}
			}
			if out.state != (gitStatus{branch: "main", upstream: "origin/main", ahead: 2, behind: 3, hasCounts: true, dirty: true}) {
				t.Fatalf("%+v", out.state)
			}
			if len(out.record) != 0 || out.skipRecord {
				t.Fatal("retained incomplete record")
			}
		})
	}
}

func TestGitStatusStreamingDiscardsPathsAndBoundsHeaders(t *testing.T) {
	for _, tag := range []string{"1 ", "2 ", "u ", "? "} {
		t.Run(tag, func(t *testing.T) {
			var out gitStatusWriter
			write := func(part string) {
				t.Helper()
				if n, err := out.Write([]byte(part)); n != len(part) || err != nil {
					t.Fatalf("Write = %d, %v", n, err)
				}
			}
			write("# branch.head main\x00" + tag)
			chunk := strings.Repeat("x", 4096)
			// Large pathnames must not accumulate in memory, even when no
			// delimiter has arrived yet.
			for range 1024 {
				write(chunk)
				if len(out.record) != 2 || cap(out.record) > 128 {
					t.Fatalf("retained pathname: len %d cap %d", len(out.record), cap(out.record))
				}
			}
			write("\x00")
			if tag == "2 " {
				write("# branch.head injected\x00")
			}
			write("# branch.ab +1 -2\x00")
			if !out.state.dirty || out.state.branch != "main" || out.state.ahead != 1 || out.state.behind != 2 {
				t.Fatalf("%+v", out.state)
			}
		})
	}
	var out gitStatusWriter
	if _, err := out.Write([]byte("#" + strings.Repeat("x", maxGitStatusHeader))); err == nil {
		t.Fatal("unbounded header accepted")
	}
	if len(out.record) > maxGitStatusHeader {
		t.Fatal("oversized header retained")
	}
}

func TestGitStatusStreamingRejectsFailedAndCanceledProcesses(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "git")
	env := baseEnv(map[string]string{"PATH": dir})
	r := &Renderer{}
	runScript := func(script string, ctx context.Context) error {
		t.Helper()
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0700); err != nil {
			t.Fatal(err)
		}
		state, err := r.gitStatus(ctx, dir, env)
		if state != (gitStatus{}) {
			t.Fatalf("failed subprocess returned partial state: %+v", state)
		}
		return err
	}
	if err := runScript("printf '# branch.head main\\000? file\\000'; exit 1\n", context.Background()); err == nil {
		t.Fatal("ignored nonzero exit after valid output")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := runScript("printf '# branch.head main\\000'; while :; do printf '? file\\000'; done\n", ctx)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatalf("stream ignored cancellation: %v", err)
	}
	if err := runScript("printf '# branch.head main'\n", context.Background()); err == nil {
		t.Fatal("accepted truncated record")
	}
}
