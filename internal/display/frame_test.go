package display

import (
	"testing"
	"time"
)

func TestReadinessRetryBudget(t *testing.T) {
	if frameRetryInterval <= 0 || frameRetryInterval > 4*time.Millisecond {
		t.Fatal("pending frame polling must not add a display refresh of latency")
	}
}

func TestResizeWakePreservesGateDeadline(t *testing.T) {
	now := time.Now()
	var gate frameGate
	for elapsed := time.Duration(0); elapsed < 300*time.Millisecond; elapsed += frameRetryInterval {
		if gate.ready(24, 12, 12, 12, now.Add(elapsed)) {
			t.Fatal("resize wake drew staged content at old dimensions", elapsed)
		}
	}
	if !gate.ready(24, 12, 12, 12, now.Add(300*time.Millisecond)) || !gate.timedOut {
		t.Fatal("frequent resize wakes reset the safety deadline")
	}
	if !gate.ready(24, 12, 24, 12, now.Add(301*time.Millisecond)) || gate.timedOut {
		t.Fatal("matching dimensions did not clear timeout")
	}
}

func TestFrameWaitsForMatchingSize(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name      string
		old, next int
	}{{"growth", 5, 20}, {"shrink", 20, 5}} {
		t.Run(tc.name, func(t *testing.T) {
			var gate frameGate
			if gate.ready(tc.next, tc.old, tc.old, tc.old, now) {
				t.Fatal("new frame drawn at old size")
			}
			if !gate.ready(tc.next, tc.old, tc.next, tc.old, now.Add(100*time.Millisecond)) {
				t.Fatal("matching frame blocked")
			}
			if gate.pending {
				t.Fatal("pending not cleared")
			}
		})
	}
}

func TestResizeDoesNotRedrawOldFrame(t *testing.T) {
	now := time.Now()
	var gate frameGate
	if gate.ready(20, 20, 5, 20, now) {
		t.Fatal("old frame redrawn after resize before new frame")
	}
	if !gate.ready(5, 20, 5, 20, now.Add(time.Millisecond)) {
		t.Fatal("new matching frame blocked")
	}
}

func TestFrameGateBoundedAndSuperseded(t *testing.T) {
	now := time.Now()
	var gate frameGate
	if gate.ready(20, 5, 5, 5, now) {
		t.Fatal("not deferred")
	}
	if gate.ready(30, 5, 5, 5, now.Add(200*time.Millisecond)) {
		t.Fatal("not deferred")
	}
	if !gate.ready(40, 5, 5, 5, now.Add(300*time.Millisecond)) {
		t.Fatal("continuous updates blocked forever")
	}
	if !gate.ready(20, 0, 5, 0, now) {
		t.Fatal("startup blocked")
	}
	if !gate.ready(5, 5, 5, 5, now) {
		t.Fatal("unchanged size blocked")
	}
}
