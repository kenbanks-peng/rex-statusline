package manager

import (
	"context"
	"os"
	"testing"

	"rex-statusline/internal/rex"
)

func TestReconcileClosesUnmatchedFloatsAndAdoptsSharedTabFloat(t *testing.T) {
	f := fixture()
	m := testManager(t, f)
	for _, id := range []string{"block:a", "block:b"} {
		f.view.Windows[0].Layers = append(f.view.Windows[0].Layers, rex.Layer{
			ID: "layer:" + id, Kind: "floating",
			Blocks: []rex.Block{{ID: "status:" + id, Creator: rex.Terminal, Label: rex.StatusPrefix + id}},
		})
		if err := WriteFrame(m.FramePath(id), "old"); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if err := m.Reconcile(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if f.closed != 2 || f.created != 1 || len(f.view.Windows[0].Layers) != 2 {
		t.Fatalf("reconciliation: closed %d created %d", f.closed, f.created)
	}
	for _, id := range []string{"block:a", "block:b"} {
		if _, err := os.Stat(m.FramePath(id)); !os.IsNotExist(err) {
			t.Fatal("unmatched frame remains", id, err)
		}
	}
	status := f.view.Windows[0].Layers[1].Blocks[0]
	if status.Label != rex.StatusPrefix+"window:w" {
		t.Fatal("float still bound to a source", status.Label)
	}
	// Restarting the manager adopts the shared float.
	m.focused, m.placements = nil, nil
	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.created != 1 || f.closed != 2 {
		t.Fatal("restart replaced shared float")
	}
}

func TestInactiveTabsPauseAndRefreshOnActivation(t *testing.T) {
	f := fixture()
	m := testManager(t, f)
	c := rex.Block{ID: "block:c", Creator: rex.Terminal}
	f.view.Windows = append(f.view.Windows, rex.Window{
		ID: "window:other", Focused: c.ID,
		Layers: []rex.Layer{{ID: "layer:other", Kind: "tiled", Blocks: []rex.Block{c},
			Layout: &rex.Node{BlockID: c.ID, Rect: rex.Rect{0, 0, 1, 1}}}},
	})
	var rendered []string
	m.Render = func(_ context.Context, source Source) (string, error) {
		rendered = append(rendered, source.Block.ID)
		return source.Block.ID, nil
	}
	check := func(active, block string) {
		t.Helper()
		f.view.ActiveWindowID = active
		rendered = nil
		sizes := len(f.sizeRequests)
		if err := m.Reconcile(context.Background()); err != nil {
			t.Fatal(err)
		}
		if len(f.sizeRequests) != sizes+1 || f.sizeRequests[sizes] != block {
			t.Fatalf("queried inactive terminal sizes: %v", f.sizeRequests[sizes:])
		}
		if len(rendered) != 1 || rendered[0] != block {
			t.Fatalf("rendered %v, want only %s", rendered, block)
		}
		data, err := os.ReadFile(m.FramePath(active))
		if err != nil || frameLine(t, data) != block {
			t.Fatalf("frame %q: %v", data, err)
		}
	}
	check("window:w", "block:a")
	if _, err := os.Stat(m.FramePath("window:other")); !os.IsNotExist(err) {
		t.Fatal("inactive tab created a frame")
	}
	check("window:other", c.ID)
	f.view.Windows[0].Focused = "block:b"
	for range 3 {
		check("window:other", c.ID)
	}
	data, err := os.ReadFile(m.FramePath("window:w"))
	if err != nil || frameLine(t, data) != "block:a" {
		t.Fatal("inactive tab's frame changed")
	}
	check("window:w", "block:b")
	if f.created != 2 || f.closed != 0 {
		t.Fatal("tab activation replaced floats")
	}
	// No known active tab means no rendering, not a refresh of every tab.
	f.view.ActiveWindowID = ""
	rendered = nil
	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(rendered) != 0 {
		t.Fatal("rendered without an active tab")
	}
	m.Cleanup(context.Background())
	if f.closed != 2 {
		t.Fatal("cleanup did not close active and inactive tab floats")
	}
}
