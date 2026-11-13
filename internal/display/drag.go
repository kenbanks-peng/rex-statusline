package display

import (
	"context"
	"fmt"
	"math"

	"rex-statusline/internal/rex"
)

type point struct{ X, Y float64 }
type pointerFunc func() (point, error)

type drag struct {
	api            rex.API
	session, block string
	pointer        pointerFunc
	active         bool
	origin         point
	bounds, last   rex.Rect
	scale          point
	layer          string
}

func (d *drag) event(ctx context.Context, event mouseEvent) error {
	if d.pointer == nil || (event.button != 0 && event.button != 32) {
		return nil
	}
	if event.released {
		defer func() { d.active = false }()
		if d.active {
			return d.move(ctx)
		}
		return nil
	}
	if event.button == 32 {
		if d.active {
			return d.move(ctx)
		}
		return nil
	}
	d.active = false
	origin, err := d.pointer()
	if err != nil {
		return err
	}
	var view rex.View
	if err := d.api.Call(ctx, d.session, "session.view", map[string]any{}, &view); err != nil {
		return err
	}
	for _, window := range view.Windows {
		for _, layer := range window.Layers {
			if layer.Kind != "floating" || len(layer.Blocks) != 1 || layer.Blocks[0].ID != d.block {
				continue
			}
			scale, err := d.windowScale(ctx, window)
			if err != nil {
				return err
			}
			d.origin, d.bounds, d.last, d.layer = origin, layer.Bounds, layer.Bounds, layer.ID
			d.scale = scale
			d.active = true
			return nil
		}
	}
	return fmt.Errorf("status floating layer not found")
}

func (d *drag) windowScale(ctx context.Context, window rex.Window) (point, error) {
	// The float's padding and clipped cell metrics exaggerate movement,
	// especially vertically. Infer window dimensions from a tiled terminal.
	for _, layer := range window.Layers {
		if layer.Kind != "tiled" || layer.State == "minimized" {
			continue
		}
		leaves := rex.Leaves(layer.Layout)
		for _, block := range layer.Blocks {
			if block.Creator != rex.Terminal || rex.IsStatus(block) {
				continue
			}
			rect, ok := leaves[block.ID]
			if !ok {
				continue
			}
			if window.Zoomed != "" {
				if window.Zoomed != block.ID {
					continue
				}
				rect = rex.Rect{0, 0, 1, 1}
			}
			if rect[2] <= 0 || rect[3] <= 0 {
				continue
			}
			var size struct {
				Columns, Rows int
				CellWidth     float64 `json:"cell_width_px"`
				CellHeight    float64 `json:"cell_height_px"`
			}
			if err := d.api.Call(ctx, d.session, "block.method", map[string]any{"block_id": block.ID, "method": "size", "args": map[string]any{}}, &size); err != nil {
				return point{}, err
			}
			width, height := float64(size.Columns)*size.CellWidth, float64(size.Rows)*size.CellHeight
			if width <= 0 || height <= 0 {
				return point{}, fmt.Errorf("cannot drag terminal with no pixel size")
			}
			return point{rect[2] / width, rect[3] / height}, nil
		}
	}
	return point{}, fmt.Errorf("cannot drag without a visible tiled terminal")
}

func (d *drag) move(ctx context.Context) error {
	p, err := d.pointer()
	if err != nil {
		return err
	}
	bounds := d.bounds
	// Pane-relative reports become stale while the pane moves. Always use the
	// screen pointer and the original grab position.
	bounds[0] = math.Max(0, math.Min(1-bounds[2], bounds[0]+(p.X-d.origin.X)*d.scale.X))
	bounds[1] = math.Max(0, math.Min(1-bounds[3], bounds[1]+(p.Y-d.origin.Y)*d.scale.Y))
	if bounds == d.last {
		return nil
	}
	if err := d.api.Call(ctx, d.session, "session.resize_layer", map[string]any{"layer_id": d.layer, "bounds": bounds}, nil); err != nil {
		return err
	}
	d.last = bounds
	return nil
}
