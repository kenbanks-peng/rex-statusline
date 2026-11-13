package manager

import (
	"context"
	"path/filepath"
	"rex-statusline/internal/placement"
	"testing"

	"rex-statusline/internal/rex"
)

func TestDraggedPlacementSurvivesReconciliation(t *testing.T) {
	f := fixture()
	m := testManager(t, f)
	ctx := context.Background()
	if err := m.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	layer := &f.view.Windows[0].Layers[1]
	layer.Bounds[0], layer.Bounds[1] = 0, .4
	dragged := layer.Bounds
	for range 3 {
		if err := m.Reconcile(ctx); err != nil {
			t.Fatal(err)
		}
		if layer.Bounds != dragged {
			t.Fatalf("dragged float snapped back: %v != %v", layer.Bounds, dragged)
		}
	}
	m.Render = func(context.Context, Source) (string, error) { return "short", nil }
	if err := m.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if layer.Bounds[0] != dragged[0] || layer.Bounds[1] != dragged[1] || layer.Bounds[2] >= dragged[2] {
		t.Fatalf("content did not shrink at dragged position: %v", layer.Bounds)
	}
	// A newly started manager adopts existing user positions.
	m.placements = nil
	if err := m.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if layer.Bounds[0] != 0 || layer.Bounds[1] != .4 {
		t.Fatalf("restart lost manual position: %v", layer.Bounds)
	}
}

func TestPlacementFollowsAutomaticAnchorUntilDragged(t *testing.T) {
	m := &Manager{}
	old := rex.Rect{.7, 0, .3, .02}
	m.remember("float", old)
	next := rex.Rect{.6, .5, .4, .02}
	if got := m.place(next); got != next {
		t.Fatalf("undragged float stopped following source: %v", got)
	}
	m.remember("float", next)
	actual := rex.Rect{.8, .99, .4, .02}
	m.position = &position{actual[0], actual[1]}
	want := rex.Rect{.5, .98, .5, .02}
	if got := m.place(rex.Rect{.5, 0, .5, .02}); got != want {
		t.Fatalf("manual placement not clamped inside window: %v", got)
	}
}

func TestConfiguredPlacementResetsExistingAndSavedPositions(t *testing.T) {
	for _, text := range []string{"top left", "top center", "top right", "bottom left", "bottom center", "bottom right"} {
		t.Run(text, func(t *testing.T) {
			ctx := context.Background()
			f := fixture()
			m := testManager(t, f)
			m.StatePath = filepath.Join(t.TempDir(), "position.json")
			if err := m.Reconcile(ctx); err != nil {
				t.Fatal(err)
			}
			f.view.Windows[0].Layers[1].Bounds[0] = .2
			f.view.Windows[0].Layers[1].Bounds[1] = .4
			if err := m.Reconcile(ctx); err != nil {
				t.Fatal(err)
			}
			p, err := placement.Parse(text)
			if err != nil {
				t.Fatal(err)
			}
			next := testManager(t, f)
			next.StatePath = m.StatePath
			next.Placement = func() placement.Anchor { return p }
			if err := next.Reconcile(ctx); err != nil {
				t.Fatal(err)
			}
			b := f.view.Windows[0].Layers[1].Bounds
			wantX := 0.0
			if p.Horizontal == "center" {
				wantX = (1 - b[2]) / 2
			}
			if p.Horizontal == "right" {
				wantX = 1 - b[2]
			}
			wantY := 1 - b[3]
			if p.Top {
				wantY = 0
			}
			if b[0] != wantX || b[1] != wantY {
				t.Fatalf("not reset: %v", b)
			}
			// An unchanged placement must keep subsequent manual drags.
			f.view.Windows[0].Layers[1].Bounds[0] = .1
			f.view.Windows[0].Layers[1].Bounds[1] = .3
			if err := next.Reconcile(ctx); err != nil {
				t.Fatal(err)
			}
			b = f.view.Windows[0].Layers[1].Bounds
			if b[0] != .1 || b[1] != .3 {
				t.Fatalf("drag lost: %v", b)
			}
		})
	}
}

func TestManualPlacementWithoutSavedPosition(t *testing.T) {
	f := fixture()
	m := testManager(t, f)
	m.Placement = func() placement.Anchor { return placement.Anchor{Manual: true} }
	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	b := f.view.Windows[0].Layers[1].Bounds
	if b[0] != 0 || b[1] != 1-b[3] {
		t.Fatalf("manual fallback: %v", b)
	}
}
