package display

import (
	"math"
	"testing"
)

func TestScreenPointer(t *testing.T) {
	pointer, err := screenPointer()
	if err != nil {
		t.Fatal(err)
	}
	p, err := pointer()
	if err != nil {
		t.Fatal(err)
	}
	if math.IsNaN(p.X) || math.IsNaN(p.Y) || math.IsInf(p.X, 0) || math.IsInf(p.Y, 0) {
		t.Fatal(p)
	}
}
