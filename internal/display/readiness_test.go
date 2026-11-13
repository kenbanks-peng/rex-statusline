package display

import (
	"context"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"testing"

	"rex-statusline/internal/timing"
)

// Run the real display loop on a child PTY without replacing the test runner's
// own stdin/stdout or signal handlers.
func TestDisplayReadinessProcess(t *testing.T) {
	if os.Getenv("REX_DISPLAY_TEST_CHILD") != "1" {
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	logger, err := timing.Open(os.Getenv("REX_DISPLAY_TEST_LOG"))
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()
	if err := Run(ctx, os.Getenv("REX_DISPLAY_TEST_FRAME"), nil, logger); err != nil {
		t.Fatal(err)
	}
}

func TestDisplayResizeReadiness(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("PTY regression requires python3")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(python, "readiness_test.py", binary)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("PTY regression: %v\n%s", err, output)
	}
	t.Logf("%s", output)
}
