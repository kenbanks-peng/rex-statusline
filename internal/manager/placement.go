package manager

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"rex-statusline/internal/rex"
)

type position struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

func StatePath() (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if !filepath.IsAbs(base) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "rex-statusline", "position.json"), nil
}

func (m *Manager) loadPosition() error {
	if m.StatePath == "" {
		return nil
	}
	data, err := os.ReadFile(m.StatePath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var p position
	if err := json.Unmarshal(data, &p); err != nil {
		return fmt.Errorf("invalid saved position: %w", err)
	}
	if math.IsNaN(p.X) || math.IsNaN(p.Y) || p.X < 0 || p.X > 1 || p.Y < 0 || p.Y > 1 {
		return fmt.Errorf("saved position must be within the window")
	}
	m.position = &p
	return nil
}

func (m *Manager) savePosition() error {
	if m.StatePath == "" || m.position == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(m.StatePath), 0700); err != nil {
		return err
	}
	data, err := json.Marshal(m.position)
	if err != nil {
		return err
	}
	return WriteFrame(m.StatePath, string(data)+"\n")
}

func (m *Manager) remember(layer string, bounds rex.Rect) {
	if m.placements == nil {
		m.placements = map[string]rex.Rect{}
	}
	m.placements[layer] = bounds
}

// Observe every session before moving any floats, so stale snapshots cannot
// mistake a synchronized move for a new user move.
func (m *Manager) observePositions(views []rex.View) {
	var adopted *position
	for _, view := range views {
		_, statuses := Sources(view)
		for _, status := range statuses {
			layer := status.layer
			if layer.Kind != "floating" || len(layer.Blocks) != 1 {
				continue
			}
			actual := layer.Bounds
			if previous, known := m.placements[layer.ID]; known {
				if math.Abs(actual[0]-previous[0]) > .00001 || math.Abs(actual[1]-previous[1]) > .00001 {
					m.position = &position{actual[0], actual[1]}
				}
			} else if m.initialPlacement.Manual && m.position == nil {
				adopted = &position{actual[0], actual[1]}
			}
		}
	}
	if m.position == nil {
		m.position = adopted
	}
}

func (m *Manager) place(automatic rex.Rect) rex.Rect {
	if m.initialPlacement.Top {
		automatic[1] = 0
	}
	switch m.initialPlacement.Horizontal {
	case "center":
		automatic[0] = (1 - automatic[2]) / 2
	case "right":
		automatic[0] = 1 - automatic[2]
	}
	if m.position != nil {
		automatic[0] = math.Max(0, math.Min(m.position.X, 1-automatic[2]))
		automatic[1] = math.Max(0, math.Min(m.position.Y, 1-automatic[3]))
	}
	return automatic
}
