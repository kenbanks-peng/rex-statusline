package rex

import (
	"fmt"
	"math"
	"sync"

	"github.com/ebitengine/purego"
)

// Rex reports physical cell pixels; saved terminal padding is in points.
// The API does not identify the display containing a client window, so use
// the main display's backing scale. Keep the library loaded for these symbols.
var displayScale = sync.OnceValues(func() (func() (float64, error), error) {
	cg, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_LAZY|purego.RTLD_LOCAL)
	if err != nil {
		return nil, err
	}
	var mainDisplay func() uint32
	var copyMode func(uint32) uintptr
	var width, pixelWidth func(uintptr) uintptr
	var release func(uintptr)
	purego.RegisterLibFunc(&mainDisplay, cg, "CGMainDisplayID")
	purego.RegisterLibFunc(&copyMode, cg, "CGDisplayCopyDisplayMode")
	purego.RegisterLibFunc(&width, cg, "CGDisplayModeGetWidth")
	purego.RegisterLibFunc(&pixelWidth, cg, "CGDisplayModeGetPixelWidth")
	purego.RegisterLibFunc(&release, cg, "CGDisplayModeRelease")
	return func() (float64, error) {
		mode := copyMode(mainDisplay())
		if mode == 0 {
			return 0, fmt.Errorf("read display scale: no display mode")
		}
		defer release(mode)
		scale := float64(pixelWidth(mode)) / float64(width(mode))
		if scale <= 0 || math.IsNaN(scale) || math.IsInf(scale, 0) {
			return 0, fmt.Errorf("invalid display scale %v", scale)
		}
		return scale, nil
	}, nil
})

func paddingInPixels(raw string, scale func() (float64, error)) (*float64, error) {
	padding, err := parsePadding(raw)
	if err != nil {
		return nil, err
	}
	factor, err := scale()
	if err != nil {
		return nil, err
	}
	value := *padding * factor
	return &value, nil
}
