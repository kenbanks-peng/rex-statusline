package manager

import (
	"context"
	"testing"
)

func TestConcurrentLayerChangePreservesUserBlock(t *testing.T) {
	f := fixture()
	m := testManager(t, f)
	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.addUserOnClose = true
	m.Cleanup(context.Background())
	found := false
	for _, w := range f.view.Windows {
		for _, l := range w.Layers {
			for _, b := range l.Blocks {
				if b.ID == "block:unrelated" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatal("concurrent block moved into status layer was closed")
	}
}
