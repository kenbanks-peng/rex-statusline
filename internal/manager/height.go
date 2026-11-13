package manager

import (
	"math"

	"rex-statusline/internal/rex"
)

// Conservative fallback when the client's padding setting is unavailable.
const heightAllowancePX = 8

func displayBounds(source Source, padding *float64) rex.Rect {
	if source.Size.Rows <= 0 || source.Size.Columns <= 0 || source.Rect[3] <= 0 {
		return rex.Rect{}
	}
	if source.Size.CellHeightPX <= 0 {
		return Bounds(source)
	}
	// A clipped float reports clipped cell metrics as well as clamped rows.
	// Only source geometry can size the float without resize feedback.
	sourceInset, displayInset := 0.0, float64(heightAllowancePX)
	if padding != nil && *padding >= 0 && !math.IsNaN(*padding) && !math.IsInf(*padding, 0) {
		sourceInset, displayInset = 2**padding, 2**padding
	}
	windowHeight := (float64(source.Size.Rows*source.Size.CellHeightPX) + sourceInset) / source.Rect[3]
	h := math.Min(1, (float64(source.Size.CellHeightPX)+displayInset)/windowHeight)
	return rex.Rect{0, 1 - h, 1, h}
}
