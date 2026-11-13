package manager

import (
	"context"
	"errors"
	"testing"

	"rex-statusline/internal/rex"
)

type snapshotAPI struct {
	rex.API
	read func(context.Context, string) ([]rex.SessionSnapshot, error)
}

func (a snapshotAPI) Snapshot(ctx context.Context, session string) ([]rex.SessionSnapshot, error) {
	return a.read(ctx, session)
}

func TestSnapshotUsesCombinedReadAndPreservesPartialFailures(t *testing.T) {
	m := Manager{Session: "selected", API: snapshotAPI{
		API: apiFunc(func(context.Context, string, string, any, any) error { t.Fatal("unexpected separate RPC"); return nil }),
		read: func(_ context.Context, session string) ([]rex.SessionSnapshot, error) {
			if session != "selected" {
				t.Fatal(session)
			}
			return []rex.SessionSnapshot{{SessionID: "a", View: rex.View{Revision: 7}}, {SessionID: "b", Error: "unavailable"}}, nil
		},
	}}
	sessions, results, err := m.snapshot(context.Background())
	if err != nil || len(sessions) != 2 || len(results) != 2 {
		t.Fatalf("%v %v %v", sessions, results, err)
	}
	if sessions[0].ID != "a" || results[0].view.SessionID != "a" || results[0].view.Revision != 7 || results[1].err == nil {
		t.Fatal(results)
	}
}

func TestFailedCombinedSnapshotPreservesExistingWindows(t *testing.T) {
	expected := errors.New("transport failed")
	m := Manager{API: snapshotAPI{API: apiFunc(func(context.Context, string, string, any, any) error { return expected }), read: func(context.Context, string) ([]rex.SessionSnapshot, error) { return nil, expected }}}
	if _, _, err := m.snapshot(context.Background()); !errors.Is(err, expected) {
		t.Fatal(err)
	}
}
