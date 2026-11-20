package rex

import (
	"fmt"
	"testing"
)

func TestPaddingInPixels(t *testing.T) {
	for _, scale := range []float64{1, 2} {
		for _, padding := range []float64{0, 2.5, 10} {
			value, err := paddingInPixels(fmt.Sprint(padding), func() (float64, error) { return scale, nil })
			if err != nil || value == nil || *value != padding*scale {
				t.Fatalf("padding=%v scale=%v: %v, %v", padding, scale, value, err)
			}
		}
	}
}

func TestPaddingScaleFailure(t *testing.T) {
	want := fmt.Errorf("display unavailable")
	value, err := paddingInPixels("10", func() (float64, error) { return 0, want })
	if value != nil || err != want {
		t.Fatalf("got %v, %v", value, err)
	}
}

func TestDisplayScale(t *testing.T) {
	read, err := displayScale()
	if err != nil {
		t.Fatal(err)
	}
	scale, err := read()
	if err != nil {
		t.Skipf("no display available: %v", err)
	}
	t.Logf("display backing scale: %g", scale)
	if scale < 1 {
		t.Fatalf("invalid backing scale %v", scale)
	}
}
