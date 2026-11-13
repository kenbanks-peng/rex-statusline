package manager

import (
	"context"
	"sync"
	"time"

	"rex-statusline/internal/timing"
)

// Preference subprocesses are independent of each other and of discovery.
// Join before applying geometry, including on discovery failure.
func (m *Manager) readPreferences(ctx context.Context, refresh bool) func() {
	type result struct {
		value *float64
		err   error
	}
	start := func(read func(context.Context) (*float64, error)) <-chan result {
		if !refresh || read == nil {
			return nil
		}
		ready := make(chan result, 1)
		go func() {
			began := time.Now()
			value, err := read(ctx)
			timing.Record(ctx, "preference_read_done", began, "ok", err == nil)
			ready <- result{value, err}
		}()
		return ready
	}
	vertical, horizontal := start(m.Padding), start(m.HorizontalPadding)
	var once sync.Once
	return func() {
		once.Do(func() {
			for _, entry := range []struct {
				ready  <-chan result
				target **float64
			}{
				{vertical, &m.padding}, {horizontal, &m.horizontalPadding},
			} {
				if entry.ready == nil {
					continue
				}
				reply := <-entry.ready
				if reply.err != nil {
					m.log(reply.err)
				} else {
					*entry.target = reply.value
				}
			}
		})
	}
}
