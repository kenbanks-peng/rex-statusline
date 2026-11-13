package bsp

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestUnsafeFiles(t *testing.T) {
	for _, kind := range []string{"fifo", "directory", "permissions", "utf8", "trailing", "unknown"} {
		t.Run(kind, func(t *testing.T) {
			s := store(t)
			if e := s.Publish("block:test", os.Getpid(), "local", contextReport()); e != nil {
				t.Fatal(e)
			}
			path := reportPath(s)
			switch kind {
			case "fifo":
				os.Remove(path)
				if e := unix.Mkfifo(path, 0600); e != nil {
					t.Fatal(e)
				}
			case "directory":
				os.Remove(path)
				os.Mkdir(path, 0700)
			case "permissions":
				os.Chmod(path, 0644)
			case "utf8":
				os.WriteFile(path, []byte{'{', 0xff, '}'}, 0600)
			case "trailing":
				f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
				f.WriteString("{}")
				f.Close()
			case "unknown":
				os.WriteFile(path, []byte("{\"unknown\":true}"), 0600)
			}
			if _, e := s.Read("block:test", process()); !errors.Is(e, ErrInvalid) {
				t.Fatalf("%v", e)
			}
		})
	}
}
func TestRootSymlinkAndLockSymlink(t *testing.T) {
	s := store(t)
	os.MkdirAll(filepath.Dir(s.Root), 0700)
	if e := os.Symlink(t.TempDir(), s.Root); e != nil {
		t.Fatal(e)
	}
	if e := s.Publish("block:test", os.Getpid(), "local", contextReport()); e == nil {
		t.Fatal("followed root symlink")
	}
	s = store(t)
	s.Publish("block:test", os.Getpid(), "local", contextReport())
	lock := filepath.Join(filepath.Dir(reportPath(s)), ".lock")
	os.Remove(lock)
	os.Symlink(reportPath(s), lock)
	if e := s.Publish("block:test", os.Getpid(), "local", contextReport()); e == nil {
		t.Fatal("followed lock symlink")
	}
}
func TestMissingDoesNotCreate(t *testing.T) {
	s := store(t)
	if _, e := s.Read("block:test", process()); !errors.Is(e, os.ErrNotExist) {
		t.Fatal(e)
	}
	if _, e := os.Stat(s.Root); !errors.Is(e, os.ErrNotExist) {
		t.Fatal(e)
	}
}
