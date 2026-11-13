package rex

import (
	"context"
	"testing"
)

func TestParsePadding(t *testing.T) {
	for _, raw := range []string{"0", "4", " 2.5\n"} {
		value, err := parsePadding(raw)
		if err != nil || value == nil || *value < 0 {
			t.Fatalf("%q: %v, %v", raw, value, err)
		}
	}
	for _, raw := range []string{"", "-1", "NaN", "+Inf", "invalid"} {
		if _, err := parsePadding(raw); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
}

func TestTerminalPaddingCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := TerminalPadding(ctx); err == nil {
		t.Fatal("ignored cancellation")
	}
}
