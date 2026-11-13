package manager

import (
	"context"
	"fmt"
	"time"

	"github.com/charmbracelet/x/ansi"
	"rex-statusline/internal/rex"
	"rex-statusline/internal/timing"
)

type renderedFrame struct {
	source       Source
	line         string
	err          error
	cachedAt     time.Time
	cachedUpdate string
}

func (m *Manager) renderFrame(ctx context.Context, source Source) renderedFrame {
	ctx = timing.WithLogger(ctx, m.Timing)
	ctx = phaseContext(sourceContext(ctx, source), "refresh")
	trace := timing.From(ctx)
	result := renderedFrame{source: source}
	refreshCtx := ctx
	beganRefresh := time.Now()
	m.Timing.Record(trace, source.WindowID, "refresh_started", time.Time{})
	defer func() {
		outcome := "rendered"
		if result.err != nil {
			outcome = "failed"
		} else if source.Hidden {
			outcome = "hidden"
		} else if result.source.Size.Columns <= 0 || result.source.Size.Rows <= 0 {
			outcome = "invalid_dimensions"
		}
		m.Timing.Record(trace, source.WindowID, "refresh_done", beganRefresh, "outcome", outcome, "ok", result.err == nil, "canceled", refreshCtx.Err() != nil, "source_columns", result.source.Size.Columns, "source_rows", result.source.Size.Rows)
	}()
	if source.Hidden {
		return result
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	render := m.Render
	if m.Prepare != nil {
		result.source.Size, render, result.err = m.Prepare(ctx, source)
	} else {
		began := time.Now()
		result.err = m.call(ctx, source.SessionID, "block.method", map[string]any{"block_id": source.Block.ID, "method": "size", "args": map[string]any{}}, &result.source.Size)
		m.Timing.Record(trace, source.WindowID, "size_done", began, "ok", result.err == nil)
	}
	if result.err == nil && result.source.Size.Columns > 0 && result.source.Size.Rows > 0 {
		input := result.source
		input.Size.Columns = displayColumns(input)
		began := time.Now()
		m.Timing.Record(trace, source.WindowID, "render_started", time.Time{})
		result.line, result.err = render(ctx, input)
		m.Timing.Record(trace, source.WindowID, "render_done", began, "ok", result.err == nil)
	}
	return result
}

func (m *Manager) commit(ctx context.Context, result renderedFrame, status *owned) (err error) {
	ctx = sourceContext(ctx, result.source)
	began := time.Now()
	trace := timing.From(ctx)
	m.Timing.Record(trace, result.source.WindowID, "commit_started", time.Time{})
	defer func() {
		outcome := "committed"
		if err != nil {
			outcome = "failed"
		} else if result.source.Hidden {
			outcome = "hidden"
		} else if result.source.Size.Columns <= 0 || result.source.Size.Rows <= 0 {
			outcome = "invalid_dimensions"
		} else if result.err != nil {
			outcome = "fallback"
		}
		m.Timing.Record(trace, result.source.WindowID, "commit_done", began, "outcome", outcome, "canceled", ctx.Err() != nil, "ok", err == nil, "render_ok", result.err == nil, "hidden", result.source.Hidden)
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	session := result.source.SessionID
	source := result.source
	if result.err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		m.log(fmt.Errorf("%s: %w", source.Block.ID, result.err))
		if source.Size.Columns <= 0 || source.Size.Rows <= 0 {
			return nil
		}
		result.line = "Status unavailable"
	}
	if source.Hidden {
		if status.window != "" && status.layer.State != "minimized" {
			if err := m.call(ctx, session, "session.set_layer_state", map[string]any{"layer_id": status.layer.ID, "state": "minimized"}, nil); err != nil {
				return err
			}
			status.layer.State = "minimized"
		}
		return nil
	}
	if source.Size.Columns <= 0 || source.Size.Rows <= 0 {
		return nil
	}
	columns := min(displayColumns(source), max(1, ansi.StringWidth(result.line)))
	bounds := displayBounds(source, m.padding)
	bounds[2] = columnsWidth(source, columns, m.horizontalPadding)
	bounds = m.place(bounds)
	if status.window == "" {
		if err := m.publish(ctx, source, result.line, columns); err != nil {
			return err
		}
		var created rex.Created
		command := []string{m.Executable, "display", "--frame", m.FramePath(source.WindowID)}
		if m.Timing != nil {
			command = append(command, "--timing-log", m.Timing.Path)
		}
		if m.DisplayServer != "" {
			command = append(command, "--server", m.DisplayServer)
		}
		requested := time.Now()
		m.Timing.Record(timing.From(ctx), source.WindowID, "create_started", time.Time{})
		err := m.call(ctx, session, "session.new_layer", map[string]any{
			"window_id": source.WindowID, "bounds": bounds, "focus": false,
			"layout": map[string]any{"block": map[string]any{
				"flavor": rex.Terminal + ".shell", "label": rex.StatusPrefix + source.WindowID,
				"options": map[string]any{"shell": "none", "command": command},
			}},
		}, &created)
		m.Timing.Record(timing.From(ctx), source.WindowID, "create_done", requested, "ok", err == nil)
		// A timeout may have created the layer. Never retry creation within this
		// snapshot; the next reconciliation discovers the server's actual state.
		if err != nil {
			return err
		}
		*status = owned{window: source.WindowID, layer: rex.Layer{ID: created.LayerID, Kind: "floating", State: "normal", Bounds: bounds}}
		m.remember(created.LayerID, bounds)
		if len(created.BlockIDs) > 0 {
			m.log(m.call(ctx, session, "session.disown_block", map[string]any{"block_ids": created.BlockIDs}, nil))
		}
	} else {
		if !same(status.layer.Bounds, bounds) {
			requested := time.Now()
			m.Timing.Record(timing.From(ctx), source.WindowID, "resize_started", time.Time{})
			err := m.call(ctx, session, "session.resize_layer", map[string]any{"layer_id": status.layer.ID, "bounds": bounds}, nil)
			m.Timing.Record(timing.From(ctx), source.WindowID, "resize_done", requested, "ok", err == nil)
			if err != nil {
				return err
			}
		}
		// Publish content and its target size only after requesting the matching bounds.
		if err := m.publish(ctx, source, result.line, columns); err != nil {
			return err
		}
		status.layer.Bounds = bounds
		m.remember(status.layer.ID, bounds)
		if status.layer.State == "minimized" {
			if err := m.call(ctx, session, "session.set_layer_state", map[string]any{"layer_id": status.layer.ID, "state": "normal"}, nil); err != nil {
				return err
			}
			status.layer.State = "normal"
		}
	}
	return ctx.Err()
}
