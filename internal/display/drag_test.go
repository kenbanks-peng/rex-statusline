package display

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"testing"

	"rex-statusline/internal/rex"
)

type dragAPI struct {
	bounds       rex.Rect
	moves, reads int
	fail         bool
	sourceRect   rex.Rect
	zoomed       bool
	noSource     bool
}

func (a *dragAPI) Call(_ context.Context, session, method string, args any, out any) error {
	if a.fail {
		return fmt.Errorf("offline")
	}
	if session != "session:test" {
		return fmt.Errorf("wrong session")
	}
	switch method {
	case "session.view":
		a.reads++
		rect := a.sourceRect
		if rect == (rex.Rect{}) {
			rect = rex.Rect{0, 0, 1, 1}
		}
		window := rex.Window{Layers: []rex.Layer{{ID: "layer:float", Kind: "floating", Bounds: a.bounds, Blocks: []rex.Block{{ID: "block:status"}}}}}
		if !a.noSource {
			window.Layers = append(window.Layers, rex.Layer{
				Kind: "tiled", Layout: &rex.Node{BlockID: "block:source", Rect: rect},
				Blocks: []rex.Block{{ID: "block:source", Creator: rex.Terminal}},
			})
		}
		if a.zoomed {
			window.Zoomed = "block:source"
		}
		*out.(*rex.View) = rex.View{Windows: []rex.Window{window}}
	case "block.method":
		a.reads++
		if args.(map[string]any)["block_id"] != "block:source" {
			return fmt.Errorf("must measure underlying terminal, not clipped float")
		}
		rect := a.sourceRect
		if rect == (rex.Rect{}) || a.zoomed {
			rect = rex.Rect{0, 0, 1, 1}
		}
		raw := fmt.Appendf(nil, `{"columns":%d,"rows":%d,"cell_width_px":10,"cell_height_px":20}`, int(100*rect[2]), int(40*rect[3]))
		return json.Unmarshal(raw, out)
	case "session.resize_layer":
		a.bounds = args.(map[string]any)["bounds"].(rex.Rect)
		a.moves++
	default:
		return fmt.Errorf("unexpected method %s", method)
	}
	return nil
}
func TestDragUsesStableScreenOrigin(t *testing.T) {
	a := &dragAPI{bounds: rex.Rect{.6, 0, .4, .025}}
	p := point{1000, 500}
	d := drag{api: a, session: "session:test", block: "block:status", pointer: func() (point, error) { return p, nil }}
	ctx := context.Background()
	event := func(button int, released bool) {
		t.Helper()
		if err := d.event(ctx, mouseEvent{button: button, released: released}); err != nil {
			t.Fatal(err)
		}
	}
	event(32, false)
	event(1, false)
	if a.reads != 0 {
		t.Fatal("motion started drag")
	}
	event(0, false)
	p = point{900, 580}
	for range 40 {
		event(32, false)
	}
	if a.moves != 1 || a.reads != 2 {
		t.Fatal(a.moves, a.reads)
	}
	if math.Abs(a.bounds[0]-.5) > 1e-9 || math.Abs(a.bounds[1]-.1) > 1e-9 {
		t.Fatal(a.bounds)
	}
	p = point{-10000, 10000}
	event(32, false)
	if a.bounds != (rex.Rect{0, .975, .4, .025}) {
		t.Fatal(a.bounds)
	}
	p = point{950, 540}
	event(0, true)
	if d.active || math.Abs(a.bounds[0]-.55) > 1e-9 || math.Abs(a.bounds[1]-.05) > 1e-9 {
		t.Fatal(a.bounds)
	}
	p = point{1100, 700}
	event(32, false)
	if a.moves != 3 {
		t.Fatal("motion after release moved float")
	}
}
func TestDragScaleIndependentOfFloatHeightAndPaneLayout(t *testing.T) {
	for _, rect := range []rex.Rect{{0, 0, 1, 1}, {.5, .5, .5, .5}} {
		for _, zoomed := range []bool{false, true} {
			for _, height := range []float64{.01, .035, .1} {
				a := &dragAPI{bounds: rex.Rect{.4, .4, .4, height}, sourceRect: rect, zoomed: zoomed}
				p := point{500, 500}
				d := drag{api: a, session: "session:test", block: "block:status", pointer: func() (point, error) { return p, nil }}
				if err := d.event(context.Background(), mouseEvent{}); err != nil {
					t.Fatal(err)
				}
				p = point{400, 420}
				if err := d.event(context.Background(), mouseEvent{button: 32}); err != nil {
					t.Fatal(err)
				}
				if math.Abs(a.bounds[0]-.3) > 1e-9 || math.Abs(a.bounds[1]-.3) > 1e-9 {
					t.Fatalf("rect=%v zoomed=%v height=%v: bounds %v", rect, zoomed, height, a.bounds)
				}
			}
		}
	}
}

func TestDragWithoutSourceDoesNotLeaveGrab(t *testing.T) {
	a := &dragAPI{bounds: rex.Rect{0, .9, 1, .1}, noSource: true}
	d := drag{api: a, session: "session:test", block: "block:status", pointer: func() (point, error) { return point{}, nil }, active: true}
	if err := d.event(context.Background(), mouseEvent{}); err == nil || d.active || a.moves != 0 {
		t.Fatal(err, d.active, a.moves)
	}
}

func TestDragPressFailureDoesNotLeaveGrab(t *testing.T) {
	a := &dragAPI{fail: true}
	d := drag{api: a, session: "session:test", block: "block:status", pointer: func() (point, error) { return point{}, nil }, active: true}
	if err := d.event(context.Background(), mouseEvent{}); err == nil || d.active {
		t.Fatal(err, d.active)
	}
}
