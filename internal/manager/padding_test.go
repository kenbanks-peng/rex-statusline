package manager

import (
	"context"
	"fmt"
	"math"
	"testing"

	"rex-statusline/internal/rex"
)

func TestHeightUsesConfiguredPadding(t *testing.T) {
	for _, padding := range []float64{0, 2.5, 12} {
		for _, rectHeight := range []float64{1, .5} {
			for _, font := range []int{15, 20, 30} {
				source := Source{
					Rect: rex.Rect{0, 0, 1, rectHeight},
					Size: rex.Size{Columns: 80, Rows: 40, CellHeightPX: font},
				}
				windowHeight := (float64(40*font) + 2*padding) / rectHeight
				bounds := displayBounds(source, &padding)
				want := (float64(font) + 2*padding) / windowHeight
				if math.Abs(bounds[3]-want) > 1e-9 || bounds[1]+bounds[3] != 1 {
					t.Fatalf("padding=%v font=%v: got %v, want height %v", padding, font, bounds, want)
				}
			}
		}
	}
}

func TestResizeDoesNotFeedClippedDisplayMetricsBackIntoHeight(t *testing.T) {
	f := fixture()
	m := testManager(t, f)
	padding := 0.0
	m.Padding = func(context.Context) (*float64, error) { return &padding, nil }
	f.sizes = map[string]rex.Size{}
	for _, step := range []struct {
		sourceRows, clippedCellHeight int
	}{
		{40, 20}, // Full height.
		{20, 10}, // The existing float is halved before reconciliation.
		{40, 15}, // Returning to full height must not preserve clipping.
		{20, 10},
		{40, 15},
	} {
		f.sizes["block:a"] = rex.Size{Columns: 80, Rows: step.sourceRows, CellHeightPX: 20}
		f.sizes["block:float1"] = rex.Size{Columns: 80, Rows: 1, CellHeightPX: step.clippedCellHeight}
		for range 3 {
			if err := m.Reconcile(context.Background()); err != nil {
				t.Fatal(err)
			}
			got := f.view.Windows[0].Layers[1].Bounds[3]
			want := 1 / float64(step.sourceRows)
			if math.Abs(got-want) > 1e-9 {
				t.Fatalf("source rows=%d, clipped display metric=%d: height=%v, want %v",
					step.sourceRows, step.clippedCellHeight, got, want)
			}
		}
	}
}

func TestPaddingRefreshAndReadFailure(t *testing.T) {
	f := fixture()
	m := testManager(t, f)
	f.sizes = map[string]rex.Size{
		"block:a":      {Columns: 80, Rows: 40, CellHeightPX: 20},
		"block:float1": {Columns: 80, Rows: 1, CellHeightPX: 20},
	}
	padding := 0.0
	var readErr error
	m.Padding = func(context.Context) (*float64, error) {
		value := padding
		return &value, readErr
	}
	check := func(want float64) {
		t.Helper()
		if err := m.Reconcile(context.Background()); err != nil {
			t.Fatal(err)
		}
		got := f.view.Windows[0].Layers[1].Bounds[3]
		if math.Abs(got-want) > 1e-9 {
			t.Fatalf("height %v, want %v", got, want)
		}
	}
	check(20.0 / 800)
	padding = 10
	check(40.0 / 820)
	padding = 0
	readErr = fmt.Errorf("preferences unavailable")
	check(40.0 / 820)
	readErr = nil
	check(20.0 / 800)
	if f.created != 1 {
		t.Fatal("padding change replaced the display")
	}
}
