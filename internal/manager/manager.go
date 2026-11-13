package manager

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"rex-statusline/internal/placement"
	"rex-statusline/internal/rex"
	"rex-statusline/internal/timing"
)

type Source struct {
	SessionID, WindowID string
	Block               rex.Block
	Rect                rex.Rect
	Size                rex.Size
	Zoomed              bool
	Hidden              bool
}
type RenderFunc func(context.Context, Source) (string, error)
type Manager struct {
	API               rex.API
	Executable        string
	DisplayServer     string
	Directory         string
	Session           string
	StatePath         string
	Placement         func() placement.Anchor
	HorizontalPadding func(context.Context) (*float64, error)
	horizontalPadding *float64
	initialized       bool
	initialPlacement  placement.Anchor
	Padding           func(context.Context) (*float64, error)
	padding           *float64
	// Prepare reads fresh dimensions and render context for one request.
	Prepare    func(context.Context, Source) (rex.Size, RenderFunc, error)
	Render     RenderFunc
	Log        func(error)
	Timing     *timing.Logger
	sessions   map[string]bool
	placements map[string]rex.Rect
	position   *position
	focused    map[string]string
	frames     map[Source]renderedFrame
}

func (m *Manager) FramePath(window string) string {
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(window)))
	return filepath.Join(m.Directory, key+".line")
}
func (m *Manager) call(ctx context.Context, session, method string, args any, out any) error {
	return m.API.Call(ctx, session, method, args, out)
}
func (m *Manager) log(err error) {
	if err != nil && m.Log != nil {
		m.Log(err)
	}
}
func (m *Manager) list(ctx context.Context) ([]rex.Session, error) {
	if m.Session != "" {
		return []rex.Session{{ID: m.Session}}, nil
	}
	var result struct {
		Sessions []rex.Session `json:"sessions"`
	}
	err := m.call(ctx, "", "session.list", map[string]any{}, &result)
	return result.Sessions, err
}
func (m *Manager) reconcile(ctx context.Context, enqueue func(context.Context, Source, owned)) (err error) {
	ctx = timing.WithLogger(ctx, m.Timing)
	began := time.Now()
	trace := timing.From(ctx)
	if trace.ID == "" {
		trace = timing.New("manual", began)
		ctx = timing.WithTrace(ctx, trace)
	}
	m.Timing.Record(trace, "", "snapshot_started", time.Time{})
	defer func() { m.Timing.Record(trace, "", "snapshot_done", began, "ok", err == nil) }()
	observed := m.readRefresh(ctx, !m.initialized || trace.Trigger != "event")
	return m.applyRefresh(ctx, observed, enqueue)
}

func (m *Manager) applyRefresh(ctx context.Context, observed refresh, enqueue func(context.Context, Source, owned)) error {
	m.padding, m.horizontalPadding = observed.padding, observed.horizontalPadding
	placement := placement.Anchor{}
	if m.Placement != nil {
		placement = m.Placement()
	}
	if !m.initialized || placement != m.initialPlacement {
		m.position = nil
		m.placements = nil
		if placement.Manual {
			if err := m.loadPosition(); err != nil {
				return err
			}
		}
		m.initialPlacement = placement
		m.initialized = true
	}
	sessions, results := observed.sessions, observed.results
	if observed.err != nil {
		return observed.err
	}
	if m.sessions == nil {
		m.sessions = map[string]bool{}
	}
	live := map[string]bool{}
	var failures []error
	var views []rex.View
	// Discovery is independent; all state observation and mutation remain ordered.
	for i, session := range sessions {
		live[session.ID] = true
		m.sessions[session.ID] = true
		if results[i].err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", session.ID, results[i].err))
			continue
		}
		views = append(views, results[i].view)
	}
	m.observePositions(views)
	if err := m.savePosition(); err != nil {
		return err
	}
	for _, view := range views {
		if err := m.reconcileSession(ctx, view, enqueue); err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", view.SessionID, err))
		}
	}
	for id := range m.sessions {
		if !live[id] {
			delete(m.sessions, id)
			for source := range m.frames {
				if source.SessionID == id {
					m.invalidateFrame(ctx, source, "session_removed")
				}
			}
		}
	}
	return errors.Join(failures...)
}

type owned struct {
	window string
	layer  rex.Layer
	block  rex.Block
}

func Sources(view rex.View) (map[string]Source, []owned) {
	sources := map[string]Source{}
	var statuses []owned
	for _, window := range view.Windows {
		for _, layer := range window.Layers {
			leaves := rex.Leaves(layer.Layout)
			for _, block := range layer.Blocks {
				if strings.HasPrefix(block.Label, rex.StatusPrefix) {
					statuses = append(statuses, owned{window.ID, layer, block})
					continue
				}
				if rex.IsStatus(block) || block.Creator != rex.Terminal {
					continue
				}
				rect, ok := leaves[block.ID]
				if !ok || rect[2] <= 0 || rect[3] <= 0 {
					continue
				}
				hidden := layer.State == "minimized" || (window.Zoomed != "" && window.Zoomed != block.ID)
				zoomed := window.Zoomed == block.ID
				if zoomed {
					rect = rex.Rect{0, 0, 1, 1}
				}
				sources[block.ID] = Source{SessionID: view.SessionID, WindowID: window.ID, Block: block, Rect: rect, Zoomed: zoomed, Hidden: hidden}
			}
		}
	}
	return sources, statuses
}

func Bounds(source Source) rex.Rect {
	rect := source.Rect
	if source.Size.Columns <= 0 || source.Size.Rows <= 0 {
		return rex.Rect{}
	}
	// Without pixel metrics, reserve a row of padding around the status row.
	h := 2 * rect[3] / float64(source.Size.Rows)
	h = math.Min(1, h)
	return rex.Rect{0, 1 - h, 1, h}
}
func same(a, b rex.Rect) bool {
	for i := range a {
		if math.Abs(a[i]-b[i]) > .00001 {
			return false
		}
	}
	return true
}
func (m *Manager) close(ctx context.Context, s string, status owned) error {
	// Never close a tiled layer or a layer containing unrelated user blocks.
	if status.layer.Kind != "floating" || len(status.layer.Blocks) != 1 {
		return fmt.Errorf("refusing to close non-dedicated status layer %s", status.layer.ID)
	}
	// A user may move another block into the layer after the snapshot. Closing
	// only our block cannot terminate that unrelated process.
	err := m.call(ctx, s, "block.close", map[string]any{"block_id": status.block.ID}, nil)
	if err == nil {
		delete(m.placements, status.layer.ID)
	}
	return err
}
func (m *Manager) reconcileSession(ctx context.Context, view rex.View, enqueue func(context.Context, Source, owned)) error {
	session := view.SessionID
	sources, statuses := Sources(view)
	m.pruneFrames(ctx, view.SessionID, sources)
	tabs := m.tabSources(view, sources)
	sort.Slice(statuses, func(i, j int) bool { return statuses[i].layer.ID < statuses[j].layer.ID })
	current := map[string]owned{}
	for _, status := range statuses {
		id := strings.TrimPrefix(status.block.Label, rex.StatusPrefix)
		_, exists := tabs[status.window]
		_, duplicate := current[status.window]
		if !exists || id != status.window || duplicate {
			if err := m.close(ctx, session, status); err != nil {
				return err
			}
			if id != status.window || !exists {
				_ = os.Remove(m.FramePath(id))
			}
			continue
		}
		if status.layer.Kind != "floating" || len(status.layer.Blocks) != 1 {
			return fmt.Errorf("status block %s is not in a dedicated floating layer", status.block.ID)
		}
		current[status.window] = status
	}
	if source, ok := tabs[view.ActiveWindowID]; ok {
		enqueue(sourceContext(ctx, source), source, current[source.WindowID])
	}
	return ctx.Err()
}

func WriteFrame(path, line string) error {
	existing, err := os.ReadFile(path)
	if err == nil && string(existing) == line {
		return nil
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".frame-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.WriteString(line); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
func (m *Manager) Cleanup(ctx context.Context) {
	var views []rex.View
	for session := range m.sessions {
		var view rex.View
		if err := m.call(ctx, session, "session.view", map[string]any{}, &view); err != nil {
			m.log(err)
			continue
		}
		view.SessionID = session
		views = append(views, view)
	}
	m.observePositions(views)
	m.log(m.savePosition())
	for _, view := range views {
		_, statuses := Sources(view)
		for _, status := range statuses {
			m.log(m.close(ctx, view.SessionID, status))
		}
	}
}
