package manager

import (
	"context"
	"math"
	"testing"

	"rex-statusline/internal/rex"
)

func TestHeightIndependentOfResizeHistory(t *testing.T) {
	for _, start := range []int{20, 40, 80} {
		for _, rows := range []int{start, 80, 20, 40, start} {
			for _, font := range []int{20, 30, 15, 20} {
				source := Source{Rect: rex.Rect{0, 0, 1, 1},
					Size: rex.Size{Columns: 80, Rows: rows, CellHeightPX: font}}
				bounds := displayBounds(source, nil)
				want := float64(font+heightAllowancePX) / float64(rows*font)
				if math.Abs(bounds[3]-want) > 1e-9 || bounds[1]+bounds[3] != 1 {
					t.Fatalf("rows=%v font=%v: bounds %v, want height %v", rows, font, bounds, want)
				}
			}
		}
	}
}

func TestHeightUsesSourceGeometry(t *testing.T) {
	want := rex.Rect{0, 1 - 28.0/800, 1, 28.0 / 800}
	for _, source := range []Source{
		{Rect: rex.Rect{0, 0, 1, 1}, Size: rex.Size{Columns: 80, Rows: 40, CellHeightPX: 20}},
		{Rect: rex.Rect{0, .5, 1, .5}, Size: rex.Size{Columns: 80, Rows: 20, CellHeightPX: 20}},
	} {
		if got := displayBounds(source, nil); !same(got, want) {
			t.Fatalf("bounds %v, want %v", got, want)
		}
	}
}

func TestHeightFallbackAndClamp(t *testing.T) {
	source := Source{Rect: rex.Rect{0, 0, 1, 1}, Size: rex.Size{Columns: 80, Rows: 40}}
	if got := displayBounds(source, nil); got != Bounds(source) {
		t.Fatal(got)
	}
	source.Size.Rows = 1
	source.Size.CellHeightPX = 20
	if got := displayBounds(source, nil); got != (rex.Rect{0, 0, 1, 1}) {
		t.Fatal(got)
	}
	if got := displayBounds(Source{}, nil); got != (rex.Rect{}) {
		t.Fatal(got)
	}
}

func TestHeightReconcileResizeAndDraggedPosition(t *testing.T) {
	f := fixture()
	m := testManager(t, f)
	f.sizes = map[string]rex.Size{
		"block:a": {Columns: 80, Rows: 40, CellHeightPX: 20},
		"block:b": {Columns: 80, Rows: 40, CellHeightPX: 20},
	}
	check := func(height float64) {
		t.Helper()
		if err := m.Reconcile(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := f.view.Windows[0].Layers[1].Bounds[3]; math.Abs(got-height) > 1e-9 {
			t.Fatalf("height %v, want %v", got, height)
		}
	}
	check(28.0 / 800)
	check(28.0 / 800)
	f.view.Windows[0].Focused = "block:b"
	check(28.0 / 800)
	if f.resized != 0 {
		t.Fatal("focus or stable geometry caused resize")
	}
	layer := &f.view.Windows[0].Layers[1]
	layer.Bounds[1] = .4
	for _, rows := range []int{10, 40, 20, 10, 20} {
		f.sizes["block:b"] = rex.Size{Columns: 80, Rows: rows, CellHeightPX: 20}
		check(28.0 / float64(rows*20))
		if layer.Bounds[1] != .4 {
			t.Fatal("lost dragged position")
		}
	}
	f.sizes["block:b"] = rex.Size{Columns: 80, Rows: 20, CellHeightPX: 30}
	check(38.0 / 600)
	if f.created != 1 {
		t.Fatal("resize replaced the display")
	}
}
