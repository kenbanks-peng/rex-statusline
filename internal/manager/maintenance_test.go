package manager

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"rex-statusline/internal/rex"
)

func TestMaintenanceReadsDoNotBlockReadyFrame(t *testing.T) {
	for _, slow := range []string{"snapshot", "preferences"} {
		t.Run(slow, func(t *testing.T) {
			f := fixture()
			m := testManager(t, f)
			schedulerLog(t, m)
			var calls atomic.Int32
			rendering, release := make(chan struct{}), make(chan struct{})
			pollStarted, pollRelease := make(chan struct{}), make(chan struct{})
			m.Render = func(ctx context.Context, _ Source) (string, error) {
				if calls.Add(1) == 1 {
					return "initial", nil
				}
				close(rendering)
				select {
				case <-release:
					return "new frame", nil
				case <-ctx.Done():
					return "", ctx.Err()
				}
			}
			blockPoll := func(ctx context.Context) {
				close(pollStarted)
				select {
				case <-pollRelease:
				case <-ctx.Done():
				}
			}
			if slow == "snapshot" {
				var reads atomic.Int32
				m.API = apiFunc(func(ctx context.Context, session, method string, args, out any) error {
					if method == "session.view" && reads.Add(1) == 3 {
						blockPoll(ctx)
					}
					return f.Call(ctx, session, method, args, out)
				})
			} else {
				var reads atomic.Int32
				m.Padding = func(ctx context.Context) (*float64, error) {
					if reads.Add(1) == 2 {
						blockPoll(ctx)
					}
					return nil, nil
				}
			}
			events, ticks, stop := startScheduler(t, m)
			eventually(t, func() bool { return hasFrame(m, "window:w", "initial") })
			events <- rex.Event{Received: time.Now()}
			<-rendering
			ticks <- time.Now()
			<-pollStarted
			close(release)
			eventually(t, func() bool { return hasFrame(m, "window:w", "new frame") })
			close(pollRelease)
			stop()
			rows := timingRows(t, m)
			started := requireLog(t, rows, "maintenance_started", map[string]any{"asynchronous": true})
			requireLog(t, rows, "maintenance_read_done", map[string]any{"update": started["update"]})
			requireLog(t, rows, "maintenance_canceled", map[string]any{"update": started["update"], "reason": "frame_committed"})
		})
	}
}

func TestEventDiscardsOlderMaintenanceObservation(t *testing.T) {
	f := fixture()
	m := testManager(t, f)
	count := schedulerLog(t, m)
	pollStarted := make(chan struct{})
	var reads atomic.Int32
	m.API = apiFunc(func(ctx context.Context, session, method string, args, out any) error {
		err := f.Call(ctx, session, method, args, out)
		if method == "session.view" && reads.Add(1) == 2 {
			close(pollStarted)
			<-ctx.Done()
			// Simulate a transport returning its old successful observation anyway.
		}
		return err
	})
	events, ticks, stop := startScheduler(t, m)
	eventually(t, func() bool { return hasFrame(m, "window:w", "status for block:a") })
	ticks <- time.Now()
	<-pollStarted
	focusPane(f, "block:b")
	focusEvent(events)
	eventually(t, func() bool { return hasFrame(m, "window:w", "status for block:b") })
	eventually(t, func() bool { return count("snapshot_discarded", "poll") == 1 })
	stop()
	rows := timingRows(t, m)
	canceled := requireLog(t, rows, "maintenance_canceled", map[string]any{"reason": "event_received"})
	requireLog(t, rows, "snapshot_discarded", map[string]any{"update": canceled["update"], "reason": "generation_changed"})
	requireLog(t, rows, "maintenance_done", map[string]any{"update": canceled["update"], "applied": false})
	if !hasFrame(m, "window:w", "status for block:b") {
		t.Fatal("old poll restored previous focus")
	}
}

func TestAppliedMaintenanceLogging(t *testing.T) {
	f := fixture()
	m := testManager(t, f)
	count := schedulerLog(t, m)
	_, ticks, stop := startScheduler(t, m)
	eventually(t, func() bool { return hasFrame(m, "window:w", "status for block:a") })
	ticks <- time.Now()
	eventually(t, func() bool { return count("maintenance_done", "poll") == 1 })
	stop()
	rows := timingRows(t, m)
	done := requireLog(t, rows, "maintenance_done", map[string]any{"applied": true, "ok": true})
	for _, stage := range []string{"maintenance_started", "maintenance_read_started", "maintenance_read_done", "maintenance_apply_started", "maintenance_apply_done", "snapshot_done"} {
		requireLog(t, rows, stage, map[string]any{"update": done["update"]})
	}
}

func TestMaintenanceSkipAndShutdownLogging(t *testing.T) {
	f := fixture()
	m := testManager(t, f)
	count := schedulerLog(t, m)
	started := make(chan struct{})
	var reads atomic.Int32
	m.API = apiFunc(func(ctx context.Context, session, method string, args, out any) error {
		if method == "session.view" && reads.Add(1) == 2 {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		}
		return f.Call(ctx, session, method, args, out)
	})
	_, ticks, stop := startScheduler(t, m)
	eventually(t, func() bool { return hasFrame(m, "window:w", "status for block:a") })
	ticks <- time.Now()
	<-started
	ticks <- time.Now()
	eventually(t, func() bool { return count("maintenance_skipped", "poll") == 1 })
	stop()
	rows := timingRows(t, m)
	canceled := requireLog(t, rows, "maintenance_canceled", map[string]any{"reason": "shutdown"})
	requireLog(t, rows, "maintenance_skipped", map[string]any{"update": canceled["update"], "reason": "poll_in_flight"})
	requireLog(t, rows, "maintenance_read_done", map[string]any{"update": canceled["update"], "canceled": true, "ok": false})
}
