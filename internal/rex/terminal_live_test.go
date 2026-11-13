package rex

import (
	"context"
	"os"
	"testing"
	"time"
)

// Opt in with identifiers from a live terminal; run with REX_SESSION unset to
// exercise the same unattached CLI environment as the background manager.
func TestTerminalLiveWithoutInheritedSession(t *testing.T) {
	session, block := os.Getenv("REX_STATUSLINE_TEST_SESSION"), os.Getenv("REX_STATUSLINE_TEST_BLOCK")
	if session == "" || block == "" {
		t.Skip("set REX_STATUSLINE_TEST_SESSION and REX_STATUSLINE_TEST_BLOCK")
	}
	t.Setenv("REX_SESSION", "")
	t.Setenv("REX_BLOCK", "")
	t.Setenv("REX_CLIENT", "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, process := range []bool{false, true} {
		result, err := (Client{}).Terminal(ctx, session, block, process)
		if err != nil {
			t.Fatal(err)
		}
		if result.Size.Columns <= 0 || result.Size.Rows <= 0 {
			t.Fatalf("invalid dimensions: %+v", result.Size)
		}
		if process && result.Process.Foreground.PID <= 0 {
			t.Fatalf("missing foreground process: %+v", result.Process)
		}
	}
}
