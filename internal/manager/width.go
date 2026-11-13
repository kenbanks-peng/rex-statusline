package manager

import "math"

func displayColumns(source Source) int {
	if source.Rect[2] <= 0 {
		return source.Size.Columns
	}
	return max(1, int(math.Floor(float64(source.Size.Columns)/source.Rect[2])))
}

func columnsWidth(source Source, columns int, padding *float64) float64 {
	if source.Size.CellWidthPX <= 0 {
		return math.Min(1, float64(columns)/float64(displayColumns(source)))
	}
	inset := float64(heightAllowancePX)
	if padding != nil {
		inset = 2 * *padding
	}
	window := (float64(source.Size.Columns*source.Size.CellWidthPX) + inset) / source.Rect[2]
	return math.Min(1, (float64(columns*source.Size.CellWidthPX)+inset)/window)
}
