package rex

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"
)

// TerminalPadding reads Rex's per-edge vertical padding in points, the same
// coordinate units Rex reports as cell_height_px. Nil means no saved setting.
func TerminalPadding(ctx context.Context) (*float64, error) {
	return terminalPadding(ctx, "Y")
}

func TerminalPaddingX(ctx context.Context) (*float64, error) {
	return terminalPadding(ctx, "X")
}

func terminalPadding(ctx context.Context, axis string) (*float64, error) {
	data, err := exec.CommandContext(ctx, "/usr/bin/defaults", "read", "com.superlogical.rex", "Workspace.terminalPadding"+axis).Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && strings.Contains(string(exit.Stderr), "does not exist") {
			return nil, nil
		}
		return nil, fmt.Errorf("read Rex padding: %w", err)
	}
	return parsePadding(string(data))
}

func parsePadding(raw string) (*float64, error) {
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return nil, fmt.Errorf("invalid Rex vertical padding %q", strings.TrimSpace(raw))
	}
	return &value, nil
}
