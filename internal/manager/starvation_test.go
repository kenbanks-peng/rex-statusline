package manager

import (
	"context"
	"testing"
	"time"

	"rex-statusline/internal/rex"
)

func TestEventsDuringEverySnapshotDoNotStarveWorkers(t *testing.T) {
	f := fixture()
	m := testManager(t, f)
	ctx, cancel := context.WithCancel(context.Background())
	events := make(chan rex.Event, 128)
	m.API = apiFunc(func(ctx context.Context, s, method string, args, out any) error {
		err := f.Call(ctx, s, method, args, out)
		if method == "session.view" {
			select {
			case events <- rex.Event{Received: time.Now().Add(-time.Second)}:
			default:
			}
		}
		return err
	})
	started := make(chan struct{}, 1)
	m.Render = func(ctx context.Context, _ Source) (string, error) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return "", ctx.Err()
	}
	done := make(chan error, 1)
	go func() { done <- m.run(ctx, events, nil) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("continuous snapshots starved rendering")
	}
}
