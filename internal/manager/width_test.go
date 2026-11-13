package manager

import (
	"context"
	"math"
	"rex-statusline/internal/rex"
	"testing"
)

func TestContentWidthReconcile(t *testing.T) {
	f := fixture()
	m := testManager(t, f)
	f.view.Windows[0].Zoomed = "block:a"
	line := "\x1b[31m界é hi\x1b[0m"
	columns := 0
	m.Render = func(_ context.Context, s Source) (string, error) { columns = s.Size.Columns; return line, nil }
	f.sizes = map[string]rex.Size{"block:a": {Columns: 80, Rows: 40, CellWidthPX: 10, CellHeightPX: 20}}
	zero := 0.0
	m.HorizontalPadding = func(context.Context) (*float64, error) { return &zero, nil }
	check := func(w float64, cols int) {
		t.Helper()
		if err := m.Reconcile(context.Background()); err != nil {
			t.Fatal(err)
		}
		bounds := f.view.Windows[0].Layers[1].Bounds
		if math.Abs(bounds[2]-w) > 1e-9 || columns != cols {
			t.Fatalf("bounds=%v columns=%v want=%v,%v", bounds, columns, w, cols)
		}
	}
	check(6.0/80, 80)
	f.view.Windows[0].Layers[1].Bounds[0] = .9
	line = "a longer status"
	check(15.0/80, 80)
	if x := f.view.Windows[0].Layers[1].Bounds[0]; x != 1-15.0/80 {
		t.Fatalf("not clamped: %v", x)
	}
	f.sizes["block:a"] = rex.Size{Columns: 160, Rows: 40, CellWidthPX: 10, CellHeightPX: 20}
	check(15.0/160, 160)
	line = ""
	check(1.0/160, 160)
	if f.created != 1 {
		t.Fatal("sizing replaced float")
	}
}

func TestWidthUsesWholeWindow(t *testing.T) {
	for _, rect := range []rex.Rect{{0, 0, 1, 1}, {.5, 0, .5, 1}, {.75, 0, .25, 1}} {
		s := Source{Rect: rect, Size: rex.Size{Columns: int(160 * rect[2])}}
		if got := displayColumns(s); got != 160 {
			t.Fatalf("columns %v", got)
		}
		if got := columnsWidth(s, 8, nil); got != .05 {
			t.Fatalf("width %v", got)
		}
	}
}

func TestColumnsWidthPaddingAndClamp(t *testing.T) {
	s := Source{Rect: rex.Rect{0, 0, .5, 1}, Size: rex.Size{Columns: 80, CellWidthPX: 10}}
	pad := 4.0
	want := 108.0 / 1616
	if got := columnsWidth(s, 10, &pad); got != want {
		t.Fatalf("padded width %v want %v", got, want)
	}
	if got := columnsWidth(s, 2000, &pad); got != 1 {
		t.Fatalf("not clamped: %v", got)
	}
}
