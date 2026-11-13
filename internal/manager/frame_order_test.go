package manager

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestCompletedPresentationCommitsOnce(t *testing.T) {
	f := fixture()
	m := testManager(t, f)
	ctx := context.Background()
	line := "initial"
	renders := 0
	m.Render = func(context.Context, Source) (string, error) { renders++; return line, nil }
	reconcile := func() {
		t.Helper()
		if err := m.Reconcile(ctx); err != nil {
			t.Fatal(err)
		}
	}
	check := func(want string) {
		t.Helper()
		data, err := os.ReadFile(m.FramePath("window:w"))
		if err != nil {
			t.Fatal(err)
		}
		if got := frameLine(t, data); got != want {
			t.Fatalf("frame %q, want %q", got, want)
		}
	}
	reconcile()
	old := "initial"
	for _, next := range []string{strings.Repeat("long", 10), "short"} {
		before := f.view.Windows[0].Layers[1].Bounds
		line = next
		f.beforeResize = func() { check(old) }
		reconcile()
		check(next)
		if f.view.Windows[0].Layers[1].Bounds == before {
			t.Fatal("bounds not committed with content")
		}
		old = next
		f.beforeResize = nil
	}
	if renders != 3 {
		t.Fatalf("render calls: %d, want 3", renders)
	}
	if f.resized != 2 {
		t.Fatalf("resizes: %d", f.resized)
	}
}
