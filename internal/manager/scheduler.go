package manager

import (
	"context"
	"errors"
	"sync"
	"time"

	"rex-statusline/internal/rex"
	"rex-statusline/internal/timing"
)

type renderRequest struct {
	ctx      context.Context
	source   Source
	status   owned
	queuedAt time.Time
}

// Reconcile is the synchronous entry point; completed windows commit independently.
func (m *Manager) Reconcile(ctx context.Context) error {
	var requests []renderRequest
	snapshotErr := m.reconcile(ctx, func(ctx context.Context, source Source, status owned) {
		requests = append(requests, renderRequest{ctx: ctx, source: source, status: status, queuedAt: time.Now()})
	})
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan struct {
		request renderRequest
		frame   renderedFrame
	}, len(requests))
	jobs := make(chan renderRequest, len(requests))
	for _, request := range requests {
		jobs <- request
	}
	close(jobs)
	var workers sync.WaitGroup
	for range min(4, len(requests)) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for request := range jobs {
				workerCtx := timing.WithTrace(ctx, timing.From(request.ctx))
				results <- struct {
					request renderRequest
					frame   renderedFrame
				}{request, m.renderFrame(workerCtx, request.source)}
			}
		}()
	}
	defer workers.Wait()
	var failures []error
	for range requests {
		result := <-results
		m.compareFrame(result.request.ctx, result.frame)
		if err := m.commit(phaseContext(result.request.ctx, "refresh"), result.frame, &result.request.status); err != nil {
			failures = append(failures, err)
		} else {
			m.cacheFrame(result.request.ctx, result.frame)
		}
	}
	return errors.Join(append(failures, snapshotErr, ctx.Err())...)
}

type windowWork struct {
	request renderRequest
	pending bool
	running bool
	removed bool
	cancel  context.CancelFunc
}

type renderCompletion struct {
	work    *windowWork
	request renderRequest
	frame   renderedFrame
	readyAt time.Time
}

func (m *Manager) Run(ctx context.Context, events <-chan rex.Event) error {
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	return m.run(ctx, events, tick.C)
}

func (m *Manager) run(ctx context.Context, events <-chan rex.Event, ticks <-chan time.Time) error {
	ctx, cancel := context.WithCancel(ctx)
	var workers sync.WaitGroup
	defer func() { cancel(); workers.Wait() }()
	// Keep one notification boundary: the producer's buffered channel remains
	// live during snapshots. A second forwarding queue adds redundant snapshots.
	nextEvent := func() (rex.Event, bool) {
		select {
		case received, ok := <-events:
			if !ok {
				events = nil
			}
			return received, ok
		default:
			return rex.Event{}, false
		}
	}
	windows := map[string]*windowWork{}
	completed := make(chan renderCompletion, 4)
	active := 0
	schedule := func(trace timing.Trace, viewOnly bool, reconcile func(func(context.Context, Source, owned)) error) error {
		desired := map[string]renderRequest{}
		err := reconcile(func(ctx context.Context, source Source, status owned) {
			desired[source.WindowID] = renderRequest{ctx: ctx, source: source, status: status, queuedAt: time.Now()}
		})
		m.log(err)
		for id, work := range windows {
			// Missing data is not proof that a window closed.
			if _, ok := desired[id]; !ok && err == nil {
				work.pending, work.removed = false, true
				if work.running {
					m.Timing.Record(timing.From(work.request.ctx), id, "render_canceled", time.Time{}, "reason", "source_removed", "caused_by", trace.ID)
					work.cancel()
				} else {
					delete(windows, id)
				}
			}
		}
		for id, request := range desired {
			work := windows[id]
			if work == nil {
				work = &windowWork{}
				windows[id] = work
			}
			// A maintenance poll must not supersede event work already in flight.
			if trace.Trigger == "poll" && (work.running || work.pending) && !work.removed {
				m.Timing.Record(timing.From(request.ctx), id, "render_skipped", time.Time{}, "reason", "event_work_in_flight", "active_update", timing.From(work.request.ctx).ID)
				continue
			}
			cached, hasCache := m.frames[frameKey(request.source)]
			reason := trace.Trigger
			if trace.Trigger == "event" {
				reason = "data_event"
				if work.request.source.Block.ID != request.source.Block.ID {
					reason = "focus_changed"
				} else if work.request.source != request.source {
					reason = "geometry_or_source_changed"
				} else if viewOnly {
					reason = "view_only_same_source"
				}
			}
			previousUpdate := ""
			if work.request.ctx != nil {
				previousUpdate = timing.From(work.request.ctx).ID
			}
			m.Timing.Record(timing.From(request.ctx), id, "source_selected", time.Time{}, "previous_block", work.request.source.Block.ID, "previous_update", previousUpdate, "source_changed", work.request.source != request.source, "reason", reason)
			cacheFields := []any{"hit", hasCache}
			if hasCache {
				cacheFields = append(cacheFields, "cached_update", cached.cachedUpdate, "cached_columns", cached.source.Size.Columns, "cached_rows", cached.source.Size.Rows, "cache_age_ms", float64(time.Since(cached.cachedAt))/float64(time.Millisecond))
			} else {
				cacheFields = append(cacheFields, "reason", "not_cached")
			}
			m.Timing.Record(timing.From(request.ctx), id, "frame_cache_lookup", time.Time{}, cacheFields...)
			unchanged := !work.removed && work.request.source == request.source
			// View-only follow-ups (including our own layer resize) do not invalidate
			// data or an in-flight refresh for the same terminal and geometry.
			if viewOnly && unchanged && (work.running || work.pending || hasCache) {
				work.request.status = request.status
				var reuseErr error
				if hasCache && !work.running && !work.pending {
					reuseErr = m.commit(phaseContext(request.ctx, "reuse"), cached, &work.request.status)
					m.log(reuseErr)
				}
				m.Timing.Record(timing.From(request.ctx), id, "render_reused", time.Time{}, "reason", "view_only_same_source", "ok", reuseErr == nil, "in_flight", work.running, "pending", work.pending, "reused_update", timing.From(work.request.ctx).ID)
				continue
			}
			if !unchanged && hasCache {
				began := time.Now()
				restoreCtx := phaseContext(request.ctx, "restore")
				m.Timing.Record(timing.From(restoreCtx), id, "frame_restore_started", time.Time{}, cacheFields...)
				err := m.commit(restoreCtx, cached, &request.status)
				m.Timing.Record(timing.From(restoreCtx), id, "frame_restore_done", began, "ok", err == nil, "cached_update", cached.cachedUpdate)
				if err != nil {
					m.log(err)
				} else {
					m.Timing.Record(timing.From(restoreCtx), id, "frame_restored", began, "cached_update", cached.cachedUpdate)
				}
			}
			// A different source cannot use the active result. Cancel its reads
			// and subprocesses now, rather than waiting to discard the frame.
			if work.running && work.request.source != request.source {
				m.Timing.Record(timing.From(work.request.ctx), id, "render_canceled", time.Time{}, "reason", "source_changed", "caused_by", trace.ID)
				work.cancel()
			}
			if work.pending {
				m.Timing.Record(timing.From(work.request.ctx), id, "render_superseded", time.Time{}, "reason", "newer_request", "superseded_by", trace.ID)
			}
			work.removed = false
			work.request = request
			work.pending = true
			m.Timing.Record(timing.From(request.ctx), id, "render_queued", time.Time{}, "in_flight", work.running, "reason", reason, "background_revalidation", !unchanged && hasCache)
		}
		return err
	}
	snapshot := func(trace timing.Trace, viewOnly bool) {
		schedule(trace, viewOnly, func(enqueue func(context.Context, Source, owned)) error {
			return m.reconcile(timing.WithTrace(ctx, trace), enqueue)
		})
	}
	type pollCompletion struct {
		trace      timing.Trace
		observed   refresh
		generation uint64
		began      time.Time
		readyAt    time.Time
	}
	polls := make(chan pollCompletion, 1)
	var generation uint64
	var pollCancel context.CancelFunc
	pollRunning := false
	var pollTrace timing.Trace
	pollCanceled := false
	cancelPoll := func(reason, causedBy string) {
		if pollCancel == nil || pollCanceled {
			return
		}
		pollCanceled = true
		m.Timing.Record(pollTrace, "", "maintenance_canceled", time.Time{}, "reason", reason, "caused_by", causedBy)
		pollCancel()
	}
	defer func() {
		cancelPoll("shutdown", "")
		for id, work := range windows {
			if work.running || work.pending {
				m.Timing.Record(timing.From(work.request.ctx), id, "render_canceled", time.Time{}, "reason", "shutdown", "in_flight", work.running, "pending", work.pending)
			}
		}
	}()
	startPoll := func() {
		if pollRunning {
			m.Timing.Record(pollTrace, "", "maintenance_skipped", time.Time{}, "reason", "poll_in_flight")
			return
		}
		pollRunning = true
		pollCtx, cancel := context.WithCancel(ctx)
		pollCancel = cancel
		trace := timing.New("poll", time.Now())
		pollTrace, pollCanceled = trace, false
		m.Timing.Record(trace, "", "maintenance_started", time.Time{}, "asynchronous", true, "generation", generation)
		pollCtx = timing.WithLogger(timing.WithTrace(pollCtx, trace), m.Timing)
		reader, version := m.refreshReader(), generation
		m.Timing.Record(trace, "", "snapshot_started", time.Time{})
		workers.Add(1)
		go func() {
			defer workers.Done()
			readBegan := time.Now()
			m.Timing.Record(trace, "", "maintenance_read_started", time.Time{}, "queue_ms", float64(time.Since(trace.Started))/float64(time.Millisecond))
			observed := reader.readRefresh(pollCtx, true)
			readyAt := time.Now()
			m.Timing.Record(trace, "", "maintenance_read_done", readBegan, "ok", observed.succeeded() && pollCtx.Err() == nil, "canceled", pollCtx.Err() != nil)
			select {
			case polls <- pollCompletion{trace, observed, version, trace.Started, readyAt}:
			case <-ctx.Done():
			}
		}()
	}
	handleEvent := func(event rex.Event) {
		generation++
		cancelPoll("event_received", timing.New("event", event.Received).ID)
		received := event.Received
		coalesced := max(1, event.Count)
		// Absorb closely spaced notifications before discovery, not after it.
		// The fixed arrival-based deadline cannot slide under sustained traffic.
		deadline := received.Add(4 * time.Millisecond)
		for range len(events) {
			if next, ok := nextEvent(); ok {
				coalesced += max(1, next.Count)
				event = event.Merge(next)
			}
		}
		if wait := time.Until(deadline); wait > 0 && events != nil {
			timer := time.NewTimer(wait)
		collect:
			for time.Now().Before(deadline) {
				select {
				case <-ctx.Done():
					timer.Stop()
					m.Timing.Record(timing.New("event", received), "", "event_canceled", time.Time{}, "reason", "shutdown", "coalesced", coalesced)
					return
				case <-timer.C:
					break collect
				case next, ok := <-events:
					if !ok {
						events = nil
						break collect
					}
					coalesced += max(1, next.Count)
					event = event.Merge(next)
				}
			}
			timer.Stop()
		}
		trace := timing.New("event", received)
		classification := "data_or_mixed"
		if event.ViewOnly {
			classification = "view_only"
		}
		m.Timing.Record(trace, "", "event_dequeued", time.Time{}, "received_at", received, "queue_ms", float64(time.Since(received))/float64(time.Millisecond), "coalesced", coalesced, "view_only", event.ViewOnly, "classification", classification, "event_types", event.Types())
		snapshot(trace, event.ViewOnly)
	}
	launch := func() {
		for _, work := range windows {
			if active == 4 {
				break
			}
			if !work.pending || work.running {
				continue
			}
			work.pending, work.running = false, true
			request := work.request
			m.Timing.Record(timing.From(request.ctx), request.source.WindowID, "render_launched", request.queuedAt, "queue_ms", float64(time.Since(request.queuedAt))/float64(time.Millisecond), "active_renders", active+1)
			workerCtx, stop := context.WithCancel(request.ctx)
			work.cancel = stop
			active++
			workers.Add(1)
			go func(work *windowWork) {
				defer workers.Done()
				frame := m.renderFrame(workerCtx, request.source)
				select {
				case completed <- renderCompletion{work, request, frame, time.Now()}:
				case <-ctx.Done():
				}
			}(work)
		}
	}
	snapshot(timing.New("startup", time.Now()), false)
	prioritySnapshots := 0
	for {
		if ctx.Err() != nil {
			return nil
		}
		// Events take priority over poll refreshes and completed, possibly stale frames.
		if prioritySnapshots < 2 {
			if received, ok := nextEvent(); ok {
				handleEvent(received)
				prioritySnapshots++
			}
		}
		// Allow one follow-up snapshot for arrivals during discovery, but never
		// let a continuous stream starve workers, completions or maintenance.
		if len(events) > 0 && prioritySnapshots < 2 {
			continue
		}
		launch()
		prioritySnapshots = 0
		select {
		case <-ctx.Done():
			return nil
		case received, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			handleEvent(received)
		case <-ticks:
			// Leave a pending event ahead of maintenance work.
			if received, ok := nextEvent(); ok {
				m.Timing.Record(timing.New("poll", time.Now()), "", "maintenance_skipped", time.Time{}, "reason", "event_pending", "caused_by", timing.New("event", received.Received).ID)
				handleEvent(received)
			} else {
				startPoll()
			}
		case poll := <-polls:
			pollRunning = false
			pollCancel()
			pollCancel = nil
			if received, ok := nextEvent(); ok {
				handleEvent(received)
			}
			if poll.generation != generation {
				m.Timing.Record(poll.trace, "", "snapshot_discarded", poll.began, "reason", "generation_changed", "observed_generation", poll.generation, "current_generation", generation)
				m.Timing.Record(poll.trace, "", "maintenance_done", poll.began, "applied", false, "ok", poll.observed.succeeded(), "reason", "generation_changed")
				continue
			}
			applyBegan := time.Now()
			m.Timing.Record(poll.trace, "", "maintenance_apply_started", time.Time{}, "completion_queue_ms", float64(time.Since(poll.readyAt))/float64(time.Millisecond))
			err := schedule(poll.trace, false, func(enqueue func(context.Context, Source, owned)) error {
				return m.applyRefresh(timing.WithLogger(timing.WithTrace(ctx, poll.trace), m.Timing), poll.observed, enqueue)
			})
			m.Timing.Record(poll.trace, "", "maintenance_apply_done", applyBegan, "ok", err == nil)
			m.Timing.Record(poll.trace, "", "snapshot_done", poll.began, "ok", err == nil)
			m.Timing.Record(poll.trace, "", "maintenance_done", poll.began, "applied", err == nil, "ok", err == nil)
		case result := <-completed:
			if received, ok := nextEvent(); ok {
				handleEvent(received)
			}
			active--
			m.Timing.Record(timing.From(result.request.ctx), result.request.source.WindowID, "render_received", result.readyAt, "completion_queue_ms", float64(time.Since(result.readyAt))/float64(time.Millisecond))
			work := result.work
			work.cancel()
			work.cancel = nil
			work.running = false
			if work.removed {
				delete(windows, result.request.source.WindowID)
			}
			if work.removed || work.pending {
				reason := "superseded"
				if work.removed {
					reason = "source_removed"
				}
				m.Timing.Record(timing.From(result.request.ctx), result.request.source.WindowID, "render_discarded", time.Time{}, "reason", reason, "superseded_by", timing.From(work.request.ctx).ID)
				continue
			}
			m.compareFrame(result.request.ctx, result.frame)
			if err := m.commit(phaseContext(result.request.ctx, "refresh"), result.frame, &work.request.status); err != nil {
				m.log(err)
			} else {
				// A poll sampled before this layer mutation must not interpret our old
				// bounds as a user drag, or bring back an obsolete focused source.
				generation++
				cancelPoll("frame_committed", timing.From(result.request.ctx).ID)
				m.cacheFrame(result.request.ctx, result.frame)
			}
		}
	}
}
