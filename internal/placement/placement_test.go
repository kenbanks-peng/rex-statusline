package placement

import "testing"

func TestParse(t *testing.T) {
	for _, vertical := range []string{"top", "bottom"} {
		for _, horizontal := range []string{"left", "center", "right"} {
			a, err := Parse(vertical + " " + horizontal)
			b, other := Parse(horizontal + " " + vertical)
			if err != nil || other != nil || a != b || a.Top != (vertical == "top") || a.Horizontal != horizontal {
				t.Fatalf("placement %s %s: %+v %+v %v %v", vertical, horizontal, a, b, err, other)
			}
		}
	}
	p, err := Parse("manual")
	if err != nil || !p.Manual {
		t.Fatalf("manual: %+v %v", p, err)
	}
	for _, value := range []string{"left", "top", "center center", "top bottom", "top left right", "manual top", "unknown"} {
		if _, err := Parse(value); err == nil {
			t.Fatalf("accepted %q", value)
		}
	}
}
