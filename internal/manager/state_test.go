package manager

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"rex-statusline/internal/placement"
	"sort"
	"testing"

	"rex-statusline/internal/rex"
)

func TestStatePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, base := range []string{"", "relative", t.TempDir()} {
		t.Run(base, func(t *testing.T) {
			t.Setenv("XDG_STATE_HOME", base)
			want := filepath.Join(home, ".local", "state", "rex-statusline", "position.json")
			if filepath.IsAbs(base) {
				want = filepath.Join(base, "rex-statusline", "position.json")
			}
			got, err := StatePath()
			if err != nil || got != want {
				t.Fatalf("path %q, %v; want %q", got, err, want)
			}
		})
	}
}

func TestPositionRestoredAfterFloatsRemoved(t *testing.T) {
	ctx := context.Background()
	f := fixture()
	m := testManager(t, f)
	m.StatePath = filepath.Join(t.TempDir(), "state", "position.json")
	if err := m.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	f.view.Windows[0].Layers[1].Bounds[0] = 0
	f.view.Windows[0].Layers[1].Bounds[1] = .4
	if err := m.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(m.StatePath)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("state permissions: %v, %v", info, err)
	}
	m.Cleanup(ctx)
	restarted := testManager(t, f)
	restarted.StatePath = m.StatePath
	restarted.Placement = func() placement.Anchor { return placement.Anchor{Manual: true} }
	if err := restarted.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	bounds := f.view.Windows[0].Layers[1].Bounds
	if bounds[0] != 0 || bounds[1] != .4 {
		t.Fatalf("position not restored: %v", bounds)
	}
}

type sessionAPI map[string]*fake

func (a sessionAPI) Call(ctx context.Context, session, method string, args, out any) error {
	if method == "session.list" {
		var ids []string
		for id := range a {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		var sessions []rex.Session
		for _, id := range ids {
			sessions = append(sessions, rex.Session{ID: id})
		}
		data, _ := json.Marshal(map[string]any{"sessions": sessions})
		return json.Unmarshal(data, out)
	}
	return a[session].Call(ctx, session, method, args, out)
}

func TestPositionSyncAcrossSessionsAndNewTabs(t *testing.T) {
	ctx := context.Background()
	a, b := fixture(), fixture()
	a.view.SessionID, b.view.SessionID = "session:a", "session:b"
	b.view.Windows[0].ID = "window:b"
	b.view.ActiveWindowID = "window:b"
	b.setLeaves()
	m := testManager(t, a)
	m.API = sessionAPI{"session:a": a, "session:b": b}
	if err := m.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	// Move the later session: even the earlier session must update this pass.
	b.view.Windows[0].Layers[1].Bounds[0] = .2
	b.view.Windows[0].Layers[1].Bounds[1] = .4
	check := func() {
		t.Helper()
		if err := m.Reconcile(ctx); err != nil {
			t.Fatal(err)
		}
		for _, f := range []*fake{a, b} {
			for _, w := range f.view.Windows {
				if w.ID != f.view.ActiveWindowID {
					continue
				}
				bounds := w.Layers[1].Bounds
				if bounds[0] != .2 || bounds[1] != .4 {
					t.Fatalf("unsynchronized %s: %v", w.ID, bounds)
				}
			}
		}
	}
	check()
	// New tabs inherit the shared position.
	block := rex.Block{ID: "block:new", Creator: rex.Terminal, HostID: "host"}
	a.view.Windows = append(a.view.Windows, rex.Window{
		ID: "window:new", Focused: block.ID,
		Layers: []rex.Layer{{ID: "layer:new", Kind: "tiled", Blocks: []rex.Block{block},
			Layout: &rex.Node{BlockID: block.ID, Rect: rex.Rect{0, 0, 1, 1}}}},
	})
	a.view.ActiveWindowID = "window:new"
	check()
	resized := a.resized + b.resized
	check()
	if a.resized+b.resized != resized {
		t.Fatal("placement synchronization is not idempotent")
	}
	// Returning to a tab refreshes its shared position.
	a.view.ActiveWindowID = "window:w"
	check()
	// Moving the other line updates active tabs in both sessions.
	a.view.Windows[0].Layers[1].Bounds[0] = .1
	a.view.Windows[0].Layers[1].Bounds[1] = .3
	if err := m.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	for _, f := range []*fake{a, b} {
		for _, w := range f.view.Windows {
			if w.ID != f.view.ActiveWindowID {
				continue
			}
			if bounds := w.Layers[1].Bounds; bounds[0] != .1 || bounds[1] != .3 {
				t.Fatalf("reverse move not synchronized: %v", bounds)
			}
		}
	}
}

func TestInvalidPositionDoesNotOverwriteState(t *testing.T) {
	for _, data := range []string{"not json", `{"x":2,"y":0}`, `{"x":0,"y":-1}`} {
		t.Run(data, func(t *testing.T) {
			f := fixture()
			m := testManager(t, f)
			m.StatePath = filepath.Join(t.TempDir(), "position.json")
			m.Placement = func() placement.Anchor { return placement.Anchor{Manual: true} }
			if err := os.WriteFile(m.StatePath, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			if err := m.Reconcile(context.Background()); err == nil {
				t.Fatal("invalid position accepted")
			}
			got, _ := os.ReadFile(m.StatePath)
			if string(got) != data || f.created != 0 {
				t.Fatal("invalid state was overwritten or floats created")
			}
		})
	}
}

func TestCleanupSavesLastDragBeforeRemoving(t *testing.T) {
	ctx := context.Background()
	f := fixture()
	m := testManager(t, f)
	m.StatePath = filepath.Join(t.TempDir(), "position.json")
	if err := m.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	f.view.Windows[0].Layers[1].Bounds[0] = .15
	f.view.Windows[0].Layers[1].Bounds[1] = .35
	m.Cleanup(ctx)
	next := testManager(t, f)
	next.StatePath = m.StatePath
	next.Placement = func() placement.Anchor { return placement.Anchor{Manual: true} }
	if err := next.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	b := f.view.Windows[0].Layers[1].Bounds
	if b[0] != .15 || b[1] != .35 {
		t.Fatalf("final drag lost: %v", b)
	}
}
