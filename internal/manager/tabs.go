package manager

import "rex-statusline/internal/rex"

// tabSources keeps the display identity independent of the selected terminal.
func (m *Manager) tabSources(view rex.View, sources map[string]Source) map[string]Source {
	if m.focused == nil {
		m.focused = map[string]string{}
	}
	tabs := map[string]Source{}
	live := map[string]bool{}
	for _, window := range view.Windows {
		live[window.ID] = true
		focused := window.Focused
		if window.Zoomed != "" {
			focused = window.Zoomed
		}
		for _, layer := range window.Layers {
			for _, block := range layer.Blocks {
				if block.ID == focused && rex.IsStatus(block) {
					focused = m.focused[window.ID]
				}
			}
		}
		selected, ok := sources[focused]
		if ok && selected.WindowID == window.ID && !selected.Hidden {
			tabs[window.ID] = selected
			m.focused[window.ID] = focused
			continue
		}
		// Keep the shared float hidden when focus has no terminal context.
		for _, source := range sources {
			if source.WindowID == window.ID {
				tabs[window.ID] = Source{SessionID: view.SessionID, WindowID: window.ID, Hidden: true}
				break
			}
		}
	}
	for window := range m.focused {
		if !live[window] {
			delete(m.focused, window)
		}
	}
	return tabs
}
