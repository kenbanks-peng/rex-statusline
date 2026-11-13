//go:build !darwin

package display

func screenPointer() (pointerFunc, error) { return nil, nil }
