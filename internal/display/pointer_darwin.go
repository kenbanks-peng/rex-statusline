package display

import (
	"fmt"

	"github.com/ebitengine/purego"
)

// CoreGraphics uses screen coordinates in points, matching Rex's cell sizes.
// purego keeps the existing CGO_ENABLED=0 builds usable.
func screenPointer() (pointerFunc, error) {
	cg, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_LAZY|purego.RTLD_LOCAL)
	if err != nil {
		return nil, err
	}
	var create func(uintptr) uintptr
	var location func(uintptr) point
	var release func(uintptr)
	purego.RegisterLibFunc(&create, cg, "CGEventCreate")
	purego.RegisterLibFunc(&location, cg, "CGEventGetLocation")
	purego.RegisterLibFunc(&release, cg, "CFRelease")
	return func() (point, error) {
		event := create(0)
		if event == 0 {
			return point{}, fmt.Errorf("cannot read screen pointer")
		}
		defer release(event)
		return location(event), nil
	}, nil
}
