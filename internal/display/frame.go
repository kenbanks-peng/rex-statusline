package display

import "time"

// Poll only while staged dimensions disagree. SIGWINCH and in-band reports
// normally wake the display directly; this covers missing or early signals.
const frameRetryInterval = 2 * time.Millisecond

// Keep the current screen until the staged frame and terminal size agree.
// External resizes or an unavailable Rex client must not block updates forever.
type frameGate struct {
	pending  bool
	timedOut bool
	since    time.Time
}

func (g *frameGate) ready(target, previousTarget, columns, previousColumns int, now time.Time) bool {
	g.timedOut = false
	matched := target == columns
	if target > previousTarget {
		matched = columns >= target
	}
	if target < previousTarget {
		matched = columns <= target
	}
	if previousTarget == 0 || matched || (target == previousTarget && columns == previousColumns) {
		g.pending = false
		return true
	}
	if !g.pending {
		g.pending, g.since = true, now
	}
	if now.Sub(g.since) < 300*time.Millisecond {
		return false
	}
	g.pending = false
	g.timedOut = true
	return true
}
