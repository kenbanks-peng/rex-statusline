package manager

import (
	"context"
	"errors"
	"testing"

	"rex-statusline/internal/rex"
)

type unavailableSession struct{ rex.API }

func (a unavailableSession) Call(ctx context.Context, session, method string, args, out any) error {
	if session == "session:b" && method == "session.view" {
		return errors.New("session unavailable")
	}
	return a.API.Call(ctx, session, method, args, out)
}

func TestUnavailableSessionDoesNotBlockHealthyWindow(t *testing.T) {
	for _, asynchronous := range []bool{false, true} {
		t.Run(map[bool]string{false: "reconcile", true: "run"}[asynchronous], func(t *testing.T) {
			a, b := fixture(), fixture()
			a.view.SessionID, b.view.SessionID = "session:a", "session:b"
			m := testManager(t, a)
			m.API = unavailableSession{sessionAPI{"session:a": a, "session:b": b}}
			if asynchronous {
				startScheduler(t, m)
			} else if err := m.Reconcile(context.Background()); err == nil {
				t.Fatal("missing session failure")
			}
			eventually(t, func() bool { return hasFrame(m, "window:w", "status for block:a") })
		})
	}
}
