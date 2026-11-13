package private

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLock(t *testing.T) {
	dir := t.TempDir()
	a, err := Lock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if b, err := Lock(dir); err == nil {
		b.Close()
		t.Fatal("duplicate manager acquired lock")
	}
	a.Close()
	b, err := Lock(dir)
	if err != nil {
		t.Fatal(err)
	}
	b.Close()
}
func TestUnsafePath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)
	if _, err := Runtime("../escape"); err == nil {
		t.Fatal("traversal")
	}
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(dir, "rex-statusline")); err != nil {
		t.Fatal(err)
	}
	if _, err := Runtime("instance"); err == nil {
		t.Fatal("symlink")
	}
}
func TestRuntime(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	path, err := Runtime("instance")
	if err != nil {
		t.Fatal(err)
	}
	stat, _ := os.Stat(path)
	if stat.Mode().Perm() != 0700 {
		t.Fatal(stat.Mode())
	}
}
