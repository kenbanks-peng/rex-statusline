//go:build !darwin && !linux

package service

import (
	"context"
	"strings"
	"testing"
)

func TestStopUnsupported(t *testing.T) {
	err := Stop(context.Background())
	if err == nil || !strings.Contains(err.Error(), "service manager") {
		t.Fatalf("expected instructions to stop the service manually, got %v", err)
	}
}
