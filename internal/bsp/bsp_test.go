package bsp

import (
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func store(t *testing.T) Store {
	t.Helper()
	return Store{Root: filepath.Join(t.TempDir(), "bsp"), ServerID: "server:test", SessionID: "session:test"}
}
func contextReport() Report {
	return Report{Kind: "context", Context: Context{Cwd: "/work", Env: map[string]string{"VIRTUAL_ENV": "/a\"b\n☃"}, Jobs: 2}}
}
func process() Process { return Process{PIDs: map[int]bool{os.Getpid(): true}, ShellPID: os.Getpid()} }
func reportPath(s Store) string {
	return filepath.Join(s.Root, s.ServerID, s.SessionID, "block:test", "report.json")
}
func TestRoundTrip(t *testing.T) {
	s := store(t)
	if e := s.Publish("block:test", os.Getpid(), "local", contextReport()); e != nil {
		t.Fatal(e)
	}
	r, e := s.Read("block:test", process())
	if e != nil {
		t.Fatal(e)
	}
	if r.Kind != "context" || r.Context.Env["VIRTUAL_ENV"] != "/a\"b\n☃" || r.Epoch == "" {
		t.Fatalf("%+v", r)
	}
	for _, p := range []string{s.Root, filepath.Dir(filepath.Dir(reportPath(s))), filepath.Dir(reportPath(s))} {
		st, e := os.Stat(p)
		if e != nil || st.Mode().Perm() != 0700 {
			t.Fatalf("%s: %v %v", p, st, e)
		}
	}
	st, _ := os.Stat(reportPath(s))
	if st.Mode().Perm() != 0600 {
		t.Fatal(st.Mode())
	}
}
func TestProcessOwnership(t *testing.T) {
	s := store(t)
	s.Publish("block:test", os.Getpid(), "local", contextReport())
	for _, p := range []Process{{PIDs: map[int]bool{}}, {PIDs: process().PIDs, ShellPID: os.Getpid() + 1}, {PIDs: process().PIDs, ShellPID: os.Getpid(), Remote: true}} {
		if _, e := s.Read("block:test", p); !errors.Is(e, ErrOwnership) {
			t.Fatalf("got %v", e)
		}
	}
}
func TestEpochHandoff(t *testing.T) {
	s := store(t)
	pid := os.Getpid()
	first, e := s.Claim("block:test", pid, "container")
	if e != nil {
		t.Fatal(e)
	}
	second, e := s.Claim("block:test", pid, "remote")
	if e != nil {
		t.Fatal(e)
	}
	if first == second {
		t.Fatal("reused epoch")
	}
	if e = s.Publish("block:test", pid, "container", Report{Kind: "line", Epoch: first, Line: "late"}); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if e = s.Release("block:test", pid, first); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if e = s.Publish("block:test", pid, "local", contextReport()); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if e = s.Publish("block:test", pid, "remote", Report{Kind: "line", Epoch: second, Line: "guest"}); e != nil {
		t.Fatal(e)
	}
	p := Process{PIDs: map[int]bool{pid: true}, ShellPID: pid + 1, Remote: true}
	if r, e := s.Read("block:test", p); e != nil || r.Line != "guest" {
		t.Fatalf("%+v %v", r, e)
	}
	if e = s.Release("block:test", pid, second); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Read("block:test", p); !errors.Is(e, os.ErrNotExist) {
		t.Fatal(e)
	}
	if e = s.Publish("block:test", pid, "remote", Report{Kind: "line", Epoch: second}); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if e = s.Publish("block:test", pid, "local", contextReport()); e != nil {
		t.Fatal(e)
	}
}
func mutate(t *testing.T, s Store, fn func(*record)) {
	t.Helper()
	fd, e := s.directory("block:test", false)
	if e != nil {
		t.Fatal(e)
	}
	defer unix.Close(fd)
	r, e := load(fd)
	if e != nil {
		t.Fatal(e)
	}
	fn(&r)
	if e = save(fd, r); e != nil {
		t.Fatal(e)
	}
}
func TestStaleAndIdentity(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*record)
		want   error
	}{
		{"expired", func(r *record) { r.Updated = time.Now().Add(-TTL - time.Second) }, os.ErrNotExist},
		{"dead", func(r *record) { r.Owner = 1 << 30 }, os.ErrNotExist},
		{"future", func(r *record) { r.Updated = time.Now().Add(time.Hour) }, ErrInvalid},
		{"server", func(r *record) { r.Server = "other" }, ErrInvalid},
		{"session", func(r *record) { r.Session = "other" }, ErrInvalid},
		{"block", func(r *record) { r.Block = "other" }, ErrInvalid},
		{"nonce", func(r *record) { r.Epoch = "wrong" }, ErrInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := store(t)
			s.Publish("block:test", os.Getpid(), "local", contextReport())
			mutate(t, s, tc.mutate)
			if _, e := s.Read("block:test", process()); !errors.Is(e, tc.want) {
				t.Fatal(e)
			}
		})
	}
	s := store(t)
	s.Claim("block:test", os.Getpid(), "remote")
	mutate(t, s, func(r *record) { r.Updated = time.Now().Add(-RemoteTTL - time.Second) })
	if _, e := s.Read("block:test", process()); !errors.Is(e, os.ErrNotExist) {
		t.Fatal(e)
	}
}
func TestBadInputAndFiles(t *testing.T) {
	for _, id := range []string{"", "..", "../escape", "a/b", "a.b"} {
		s := store(t)
		if e := s.Publish(id, os.Getpid(), "local", contextReport()); !errors.Is(e, ErrInvalid) {
			t.Fatalf("%q %v", id, e)
		}
	}
	for _, data := range []string{"{}", "{", string(make([]byte, MaxBytes+1))} {
		s := store(t)
		s.Publish("block:test", os.Getpid(), "local", contextReport())
		if e := os.WriteFile(reportPath(s), []byte(data), 0600); e != nil {
			t.Fatal(e)
		}
		if _, e := s.Read("block:test", process()); !errors.Is(e, ErrInvalid) {
			t.Fatal(e)
		}
	}
	for _, r := range []Report{{Kind: "context", Context: Context{Cwd: "relative"}}, {Kind: "line", Context: Context{Cwd: "/host"}}, {Kind: "unknown"}, {Kind: "context", Context: Context{Cwd: "/", Jobs: -1}}} {
		if e := store(t).Publish("block:test", os.Getpid(), "local", r); !errors.Is(e, ErrInvalid) {
			t.Fatal(e)
		}
	}
}
func TestSymlinksAndPermissions(t *testing.T) {
	s := store(t)
	s.Publish("block:test", os.Getpid(), "local", contextReport())
	target := filepath.Join(t.TempDir(), "target")
	os.WriteFile(target, []byte("untouched"), 0600)
	os.Remove(reportPath(s))
	os.Symlink(target, reportPath(s))
	if _, e := s.Read("block:test", process()); e == nil {
		t.Fatal("read symlink")
	}
	if e := s.Publish("block:test", os.Getpid(), "local", contextReport()); e == nil {
		t.Fatal("publish followed symlink")
	}
	b, _ := os.ReadFile(target)
	if string(b) != "untouched" {
		t.Fatal("target changed")
	}
	s = store(t)
	os.MkdirAll(s.Root, 0700)
	os.Symlink(t.TempDir(), filepath.Join(s.Root, s.ServerID))
	if e := s.Publish("block:test", os.Getpid(), "local", contextReport()); e == nil {
		t.Fatal("followed directory symlink")
	}
	s = store(t)
	os.MkdirAll(s.Root, 0755)
	if e := s.Publish("block:test", os.Getpid(), "local", contextReport()); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
}
func TestAtomicPublication(t *testing.T) {
	s := store(t)
	pid := os.Getpid()
	if e := s.Publish("block:test", pid, "local", contextReport()); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 25; j++ {
				r := contextReport()
				r.Context.Cwd = fmt.Sprintf("/work/%d/%d", i, j)
				if e := s.Publish("block:test", pid, "local", r); e != nil {
					t.Error(e)
				}
				if _, e := s.Read("block:test", process()); e != nil {
					t.Error(e)
				}
			}
		}(i)
	}
	wg.Wait()
}
