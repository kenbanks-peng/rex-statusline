package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"rex-statusline/internal/rex"
)

type fake struct {
	mu                       sync.Mutex
	view                     rex.View
	created, closed, resized int
	failCreation             bool
	movedFocus               bool
	addUserOnClose           bool
	beforeResize             func()
	sizes                    map[string]rex.Size
	sizeErrors               map[string]error
	sizeRequests             []string
}

func fixture() *fake {
	a := rex.Block{ID: "block:a", Creator: rex.Terminal, HostID: "host"}
	b := rex.Block{ID: "block:b", Creator: rex.Terminal, HostID: "host"}
	return &fake{view: rex.View{SessionID: "session:s", ActiveWindowID: "window:w", Windows: []rex.Window{{ID: "window:w", Focused: a.ID, Layers: []rex.Layer{{ID: "layer:tiled", Kind: "tiled", Bounds: rex.Rect{0, 0, 1, 1}, Blocks: []rex.Block{a, b}, Layout: &rex.Node{Rect: rex.Rect{0, 0, 1, 1}}}}}}}}
}
func (f *fake) setLeaves() {
	l := &f.view.Windows[0].Layers[0]
	raw := `{"rect":[0,0,1,1],"split":{"before":{"block_id":"block:a","rect":[0,0,0.5,1]},"after":{"block_id":"block:b","rect":[0.5,0,0.5,1]}}}`
	json.Unmarshal([]byte(raw), &l.Layout)
}
func (f *fake) Call(_ context.Context, s, method string, args any, out any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	raw, _ := json.Marshal(args)
	a := map[string]json.RawMessage{}
	json.Unmarshal(raw, &a)
	str := func(key string) string { var v string; json.Unmarshal(a[key], &v); return v }
	put := func(v any) error {
		if out == nil {
			return nil
		}
		raw, _ := json.Marshal(v)
		return json.Unmarshal(raw, out)
	}
	switch method {
	case "session.view":
		return put(f.view)
	case "session.list":
		return put(map[string]any{"sessions": []rex.Session{{ID: f.view.SessionID}}})
	case "block.method":
		if str("method") != "size" {
			return fmt.Errorf("unsupported block method")
		}
		f.sizeRequests = append(f.sizeRequests, str("block_id"))
		if err := f.sizeErrors[str("block_id")]; err != nil {
			return err
		}
		if size, ok := f.sizes[str("block_id")]; ok {
			return put(size)
		}
		if strings.HasPrefix(str("block_id"), "block:float") {
			return put(rex.Size{Columns: 80, Rows: 1})
		}
		return put(rex.Size{Columns: 80, Rows: 24})
	case "session.new_layer":
		f.created++
		var bounds rex.Rect
		json.Unmarshal(a["bounds"], &bounds)
		var layout struct{ Block struct{ Label string } }
		json.Unmarshal(a["layout"], &layout)
		var focus bool
		json.Unmarshal(a["focus"], &focus)
		f.movedFocus = focus
		l := rex.Layer{ID: fmt.Sprintf("layer:%s:float%d", s, f.created), Kind: "floating", Bounds: bounds,
			Blocks: []rex.Block{{ID: fmt.Sprint("block:float", f.created), Creator: rex.Terminal, Label: layout.Block.Label}}}
		for i := range f.view.Windows {
			if f.view.Windows[i].ID == str("window_id") {
				f.view.Windows[i].Layers = append(f.view.Windows[i].Layers, l)
			}
		}
		if f.failCreation {
			f.failCreation = false
			return fmt.Errorf("timeout after creation")
		}
		return put(rex.Created{LayerID: l.ID, BlockIDs: []string{l.Blocks[0].ID}})
	case "block.close":
		for i := range f.view.Windows {
			w := &f.view.Windows[i]
			for j, l := range w.Layers {
				if len(l.Blocks) > 0 && l.Blocks[0].ID == str("block_id") {
					if f.addUserOnClose {
						l.Blocks = append(l.Blocks, rex.Block{ID: "block:unrelated"})
						f.addUserOnClose = false
					}
					if len(l.Blocks) > 1 {
						w.Layers[j].Blocks = l.Blocks[1:]
					} else {
						w.Layers = append(w.Layers[:j], w.Layers[j+1:]...)
					}
					f.closed++
					return nil
				}
			}
		}
		return fmt.Errorf("not found")
	case "session.resize_layer", "session.set_layer_state":
		if method == "session.resize_layer" && f.beforeResize != nil {
			f.beforeResize()
		}
		for i := range f.view.Windows {
			for j := range f.view.Windows[i].Layers {
				l := &f.view.Windows[i].Layers[j]
				if l.ID != str("layer_id") {
					continue
				}
				if method == "session.resize_layer" {
					json.Unmarshal(a["bounds"], &l.Bounds)
					f.resized++
				} else {
					l.State = str("state")
				}
			}
		}
	case "session.disown_block":
	default:
		return fmt.Errorf("unexpected method %s", method)
	}
	return nil
}
func testManager(t *testing.T, f *fake) *Manager {
	t.Helper()
	f.setLeaves()
	return &Manager{API: f, Executable: "/test/rex-statusline", Directory: t.TempDir(), Render: func(_ context.Context, s Source) (string, error) { return "status for " + s.Block.ID, nil }}
}
func TestLifecycle(t *testing.T) {
	ctx := context.Background()
	f := fixture()
	m := testManager(t, f)
	var rendered []string
	m.Render = func(_ context.Context, source Source) (string, error) {
		rendered = append(rendered, source.Block.ID)
		return "status for " + source.Block.ID, nil
	}
	check := func(want string) {
		t.Helper()
		rendered = nil
		if err := m.Reconcile(ctx); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(m.FramePath("window:w"))
		if err != nil || frameLine(t, data) != "status for "+want {
			t.Fatalf("frame %q: %v", data, err)
		}
		if len(rendered) != 1 || rendered[0] != want {
			t.Fatalf("rendered unfocused splits: %v", rendered)
		}
		if f.created != 1 || f.closed != 0 || f.movedFocus {
			t.Fatalf("created %d closed %d focus %v", f.created, f.closed, f.movedFocus)
		}
	}
	check("block:a")
	original := f.view.Windows[0].Layers[1]
	if want := (rex.Rect{0, 1 - 2.0/24, 18.0 / 160, 2.0 / 24}); original.Bounds != want {
		t.Fatalf("initial float bounds: %v, want %v", original.Bounds, want)
	}
	check("block:a")
	if f.resized != 0 {
		t.Fatal("not idempotent")
	}
	f.view.Windows[0].Focused = "block:b"
	check("block:b")
	f.view.Windows[0].Zoomed = "block:b"
	check("block:b")
	f.view.Windows[0].Zoomed = ""
	f.view.Windows[0].Focused = original.Blocks[0].ID
	check("block:b")
	f.view.Windows[0].Focused = "block:a"
	// Closing another split must not replace the tab's float.
	f.view.Windows[0].Layers[0].Blocks = f.view.Windows[0].Layers[0].Blocks[:1]
	check("block:a")
	if f.view.Windows[0].Layers[1].ID != original.ID {
		t.Fatal("shared float replaced")
	}
	// Losing terminal focus hides the float, and returning restores it.
	f.view.Windows[0].Focused = "block:nonterminal"
	if err := m.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if f.view.Windows[0].Layers[1].State != "minimized" {
		t.Fatal("stale terminal status remains visible")
	}
	f.view.Windows[0].Focused = "block:a"
	check("block:a")
	if f.view.Windows[0].Layers[1].State == "minimized" {
		t.Fatal("shared float not restored")
	}
	f.view.Windows[0].Layers[0].Blocks = nil
	if err := m.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if f.closed != 1 {
		t.Fatal("empty tab float not removed")
	}
	if _, err := os.Stat(m.FramePath("window:w")); !os.IsNotExist(err) {
		t.Fatal("orphan frame remains")
	}
	m.Cleanup(ctx)
	if f.closed != 1 {
		t.Fatal("cleanup closed unrelated block")
	}
}
func TestDuplicatesAndTimeout(t *testing.T) {
	f := fixture()
	m := testManager(t, f)
	f.failCreation = true
	if err := m.Reconcile(context.Background()); err == nil {
		t.Fatal("missing timeout")
	}
	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.created != 1 {
		t.Fatal("created duplicate after timeout")
	}
	duplicate := f.view.Windows[0].Layers[1]
	duplicate.ID = "layer:duplicate"
	duplicate.Blocks = append([]rex.Block(nil), duplicate.Blocks...)
	duplicate.Blocks[0].ID = "block:duplicate"
	f.view.Windows[0].Layers = append(f.view.Windows[0].Layers, duplicate)
	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.closed != 1 {
		t.Fatal("duplicate remains")
	}
}
func TestMoveAndSafeClosure(t *testing.T) {
	f := fixture()
	m := testManager(t, f)
	ctx := context.Background()
	if err := m.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	source := f.view.Windows[0].Layers[0].Blocks[1]
	f.view.Windows[0].Layers[0].Blocks = f.view.Windows[0].Layers[0].Blocks[:1]
	f.view.Windows = append(f.view.Windows, rex.Window{ID: "window:new", Focused: source.ID, Layers: []rex.Layer{{ID: "layer:new", Kind: "tiled", Blocks: []rex.Block{source}, Layout: &rex.Node{BlockID: source.ID, Rect: rex.Rect{0, 0, 1, 1}}}}})
	if err := m.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if f.closed != 0 || f.created != 1 {
		t.Fatalf("inactive tab: %d closed %d created", f.closed, f.created)
	}
	f.view.ActiveWindowID = "window:new"
	if err := m.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if f.closed != 0 || f.created != 2 {
		t.Fatalf("move: %d closed %d created", f.closed, f.created)
	}
	// Accidentally tiled/merged status blocks must not close unrelated work.
	f.view.Windows[1].Layers[1].Blocks = append(f.view.Windows[1].Layers[1].Blocks, rex.Block{ID: "block:user"})
	f.view.Windows[1].Layers[0].Blocks = nil
	err := m.Reconcile(ctx)
	if err == nil || !strings.Contains(err.Error(), "non-dedicated") {
		t.Fatalf("unsafe closure: %v", err)
	}
}
func TestBounds(t *testing.T) {
	for _, rect := range []rex.Rect{{0, 0, 1, 1}, {.5, 0, .5, 1}, {.5, .5, .5, .5}} {
		s := Source{Rect: rect, Size: rex.Size{Columns: 80, Rows: 20}}
		h := 2 * rect[3] / 20
		if got, want := Bounds(s), (rex.Rect{0, 1 - h, 1, h}); got != want {
			t.Fatalf("pane %v: got %v, want %v", rect, got, want)
		}
	}
	if got := Bounds(Source{}); got != (rex.Rect{}) {
		t.Fatal(got)
	}
}
