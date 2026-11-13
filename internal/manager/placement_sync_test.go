package manager

import (
	"context"
	"testing"
)

func TestClampedLineDoesNotMoveSharedPosition(t *testing.T) {
	a, b := fixture(), fixture()
	a.view.SessionID, b.view.SessionID = "session:a", "session:b"
	b.view.Windows[0].ID = "window:b"
	b.view.ActiveWindowID = "window:b"
	b.setLeaves()
	m := testManager(t, a)
	m.API = sessionAPI{"session:a": a, "session:b": b}
	m.position = &position{.8, .4}
	m.initialized = true
	m.Render = func(_ context.Context, s Source) (string, error) {
		if s.SessionID == "session:a" {
			return "a", nil
		}
		return "a much longer status line that must clamp", nil
	}
	ctx := context.Background()
	for range 3 {
		if err := m.Reconcile(ctx); err != nil {
			t.Fatal(err)
		}
		if m.position.X != .8 || m.position.Y != .4 {
			t.Fatalf("clamping changed shared position: %+v", m.position)
		}
		for _, f := range []*fake{a, b} {
			bounds := f.view.Windows[0].Layers[1].Bounds
			if bounds[0]+bounds[2] > 1.00001 || bounds[1] != .4 {
				t.Fatalf("invalid clamped placement: %v", bounds)
			}
		}
	}
	bounds := b.view.Windows[0].Layers[1].Bounds
	if bounds[0] != 1-bounds[2] {
		t.Fatalf("long line not clamped: %v", bounds)
	}
}
