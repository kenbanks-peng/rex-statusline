package manager

import (
	"context"
	"encoding/json"
	"os"
	"time"

	"rex-statusline/internal/presentation"
	"rex-statusline/internal/timing"
)

// Compare visible data, not tracing metadata, to avoid self-triggered updates.
func (m *Manager) publish(ctx context.Context, source Source, line string, columns int) error {
	began := time.Now()
	trace := timing.From(ctx)
	path := m.FramePath(source.WindowID)
	reason := "missing_or_invalid_frame"
	if data, err := os.ReadFile(path); err == nil {
		var previous presentation.Frame
		if json.Unmarshal(data, &previous) == nil {
			reason = "content_changed"
			if previous.Line == line {
				reason = "columns_changed"
			} else if previous.Columns != columns {
				reason = "content_and_columns_changed"
			}
			if previous.Line == line && previous.Columns == columns {
				m.Timing.Record(trace, source.WindowID, "frame_unchanged", began, "target_columns", columns)
				return nil
			}
		}
	}
	var metadata *timing.Trace
	if m.Timing != nil {
		trace.Window = source.WindowID
		metadata = &trace
	}
	encoded, err := presentation.Encode(line, columns, metadata)
	if err != nil {
		return err
	}
	if err := WriteFrame(path, encoded); err != nil {
		return err
	}
	m.Timing.Record(trace, source.WindowID, "frame_published", began, "target_columns", columns, "reason", reason)
	return nil
}

func sourceContext(ctx context.Context, source Source) context.Context {
	trace := timing.From(ctx)
	trace.Window, trace.Session, trace.Block = source.WindowID, source.SessionID, source.Block.ID
	return timing.WithTrace(ctx, trace)
}

func phaseContext(ctx context.Context, phase string) context.Context {
	trace := timing.From(ctx)
	trace.Phase = phase
	return timing.WithTrace(ctx, trace)
}
