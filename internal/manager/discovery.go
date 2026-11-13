package manager

import (
	"context"
	"errors"
	"sync"

	"rex-statusline/internal/rex"
)

// snapshot uses one CLI process for session enumeration and views when available.
func (m *Manager) snapshot(ctx context.Context) ([]rex.Session, []discovery, error) {
	if api, ok := m.API.(interface {
		Snapshot(context.Context, string) ([]rex.SessionSnapshot, error)
	}); ok {
		snapshots, err := api.Snapshot(ctx, m.Session)
		if err != nil {
			// A failed combined process must not hide healthy sessions. Retry with
			// independent, bounded reads so each session keeps its own deadline.
			m.log(err)
			sessions, listErr := m.list(ctx)
			if listErr != nil {
				return nil, nil, listErr
			}
			return sessions, m.discover(ctx, sessions), nil
		}
		sessions := make([]rex.Session, len(snapshots))
		results := make([]discovery, len(snapshots))
		for i, snapshot := range snapshots {
			sessions[i].ID = snapshot.SessionID
			results[i].view = snapshot.View
			results[i].view.SessionID = snapshot.SessionID
			if snapshot.Error != "" {
				results[i].err = errors.New(snapshot.Error)
			}
		}
		return sessions, results, nil
	}
	sessions, err := m.list(ctx)
	if err != nil {
		return nil, nil, err
	}
	return sessions, m.discover(ctx, sessions), nil
}

type discovery struct {
	view rex.View
	err  error
}

// discover preserves the server's session order, independent of reply order.
func (m *Manager) discover(ctx context.Context, sessions []rex.Session) []discovery {
	results := make([]discovery, len(sessions))
	jobs := make(chan int, len(sessions))
	for i := range sessions {
		jobs <- i
	}
	close(jobs)
	var workers sync.WaitGroup
	for range min(4, len(sessions)) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := range jobs {
				if err := ctx.Err(); err != nil {
					results[i].err = err
					continue
				}
				session := sessions[i].ID
				results[i].err = m.call(ctx, session, "session.view", map[string]any{}, &results[i].view)
				results[i].view.SessionID = session
			}
		}()
	}
	workers.Wait()
	return results
}

// refresh contains observations only. Applying them is confined to the scheduler.
type refresh struct {
	sessions                   []rex.Session
	results                    []discovery
	padding, horizontalPadding *float64
	err                        error
}

func (m *Manager) refreshReader() *Manager {
	return &Manager{API: m.API, Session: m.Session, Timing: m.Timing, Log: m.Log,
		Padding: m.Padding, HorizontalPadding: m.HorizontalPadding,
		padding: m.padding, horizontalPadding: m.horizontalPadding}
}

func (m *Manager) readRefresh(ctx context.Context, preferences bool) refresh {
	reader := m.refreshReader()
	finish := reader.readPreferences(ctx, preferences)
	sessions, results, err := reader.snapshot(ctx)
	finish()
	return refresh{sessions, results, reader.padding, reader.horizontalPadding, err}
}

func (r refresh) succeeded() bool {
	if r.err != nil {
		return false
	}
	for _, result := range r.results {
		if result.err != nil {
			return false
		}
	}
	return true
}
