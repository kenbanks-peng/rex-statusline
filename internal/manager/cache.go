package manager

import (
	"context"
	"rex-statusline/internal/rex"
	"rex-statusline/internal/timing"
	"time"
)

// Size is read during preparation, rather than discovery. Geometry and terminal
// identity remain part of the key; changed layouts cannot restore an old frame.
func frameKey(source Source) Source {
	source.Size = rex.Size{}
	return source
}

func (m *Manager) cacheFrame(ctx context.Context, frame renderedFrame) {
	reason := ""
	if frame.err != nil {
		reason = "render_failed"
	} else if frame.source.Hidden {
		reason = "hidden"
	} else if frame.source.Size.Columns <= 0 || frame.source.Size.Rows <= 0 {
		reason = "invalid_dimensions"
	}
	trace := timing.From(sourceContext(ctx, frame.source))
	if reason != "" {
		m.Timing.Record(trace, frame.source.WindowID, "frame_cache_skipped", time.Time{}, "reason", reason)
		return
	}
	if m.frames == nil {
		m.frames = map[Source]renderedFrame{}
	}
	frame.cachedAt, frame.cachedUpdate = time.Now(), trace.ID
	m.frames[frameKey(frame.source)] = frame
	m.Timing.Record(trace, frame.source.WindowID, "frame_cache_stored", time.Time{}, "source_columns", frame.source.Size.Columns, "source_rows", frame.source.Size.Rows)
}

func (m *Manager) pruneFrames(ctx context.Context, session string, sources map[string]Source) {
	for key := range m.frames {
		if key.SessionID != session {
			continue
		}
		source, exists := sources[key.Block.ID]
		if !exists || frameKey(source) != key {
			reason := "source_changed"
			if !exists {
				reason = "pane_removed"
			} else if source.Rect != key.Rect || source.Zoomed != key.Zoomed || source.Hidden != key.Hidden {
				reason = "geometry_or_visibility_changed"
			}
			m.invalidateFrame(ctx, key, reason)
		}
	}
}

func (m *Manager) invalidateFrame(ctx context.Context, key Source, reason string) {
	frame := m.frames[key]
	delete(m.frames, key)
	m.Timing.Record(timing.From(sourceContext(ctx, key)), key.WindowID, "frame_cache_invalidated", time.Time{}, "reason", reason, "cached_update", frame.cachedUpdate, "cache_age_ms", float64(time.Since(frame.cachedAt))/float64(time.Millisecond))
}

func (m *Manager) compareFrame(ctx context.Context, frame renderedFrame) {
	previous, hit := m.frames[frameKey(frame.source)]
	if !hit || frame.err != nil {
		return
	}
	m.Timing.Record(timing.From(sourceContext(ctx, frame.source)), frame.source.WindowID, "refresh_compared", time.Time{},
		"content_changed", previous.line != frame.line, "dimensions_changed", previous.source.Size != frame.source.Size, "cached_update", previous.cachedUpdate)
}
