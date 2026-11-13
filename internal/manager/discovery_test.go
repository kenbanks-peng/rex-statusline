package manager

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"rex-statusline/internal/rex"
)

type apiFunc func(context.Context, string, string, any, any) error

func (f apiFunc) Call(ctx context.Context, session, method string, args, out any) error {
	return f(ctx, session, method, args, out)
}

func TestDiscoveryBoundedOrderedAndJoined(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	started := make(chan string, 8)
	release := make(chan struct{})
	var active, peak atomic.Int32
	m := Manager{API: apiFunc(func(ctx context.Context, session, method string, args, out any) error {
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		started <- session
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
		if session == "s2" {
			return errors.New("unavailable")
		}
		out.(*rex.View).Revision = 7
		return nil
	})}
	var sessions []rex.Session
	for i := range 8 {
		sessions = append(sessions, rex.Session{ID: fmt.Sprint("s", i)})
	}
	done := make(chan []discovery, 1)
	go func() { done <- m.discover(ctx, sessions) }()
	for range 4 {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("discovery was not parallel")
		}
	}
	if active.Load() != 4 {
		t.Fatalf("active %d", active.Load())
	}
	close(release)
	results := <-done
	if active.Load() != 0 || peak.Load() != 4 {
		t.Fatalf("active %d peak %d", active.Load(), peak.Load())
	}
	for i, result := range results {
		if result.view.SessionID != sessions[i].ID {
			t.Fatalf("result %d out of order", i)
		}
		if (result.err != nil) != (i == 2) {
			t.Fatalf("result %d error: %v", i, result.err)
		}
	}
}

func TestDiscoveryCancellationDoesNotStartQueuedCalls(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var calls atomic.Int32
	m := Manager{API: apiFunc(func(ctx context.Context, _, _ string, _, _ any) error {
		calls.Add(1)
		cancel()
		<-ctx.Done()
		return ctx.Err()
	})}
	results := m.discover(ctx, make([]rex.Session, 100))
	if calls.Load() > 4 {
		t.Fatalf("calls after cancellation: %d", calls.Load())
	}
	for _, result := range results {
		if !errors.Is(result.err, context.Canceled) {
			t.Fatal(result.err)
		}
	}
}

func BenchmarkDiscovery(b *testing.B) {
	sessions := make([]rex.Session, 8)
	m := Manager{API: apiFunc(func(ctx context.Context, _, _ string, _, _ any) error {
		time.Sleep(time.Millisecond)
		return nil
	})}
	for _, parallel := range []bool{false, true} {
		b.Run(fmt.Sprint("parallel=", parallel), func(b *testing.B) {
			for range b.N {
				if parallel {
					m.discover(context.Background(), sessions)
				} else {
					for range sessions {
						m.API.Call(context.Background(), "", "session.view", nil, nil)
					}
				}
			}
		})
	}
}
