package rex

import (
	"encoding/json"
	"testing"
)

func TestViewGeometry(t *testing.T) {
	var v View
	err := json.Unmarshal([]byte(`{"session_id":"s","windows":[{"window_id":"w","layers":[{"kind":"tiled","layout":{"rect":[0,0,1,1],"split":{"before":{"block_id":"a","rect":[0,0,0.5,1]},"after":{"block_id":"b","rect":[0.5,0,0.5,1]}}}}]}]}`), &v)
	if err != nil {
		t.Fatal(err)
	}
	nodes := Leaves(v.Windows[0].Layers[0].Layout)
	if len(nodes) != 2 || nodes["b"] != (Rect{.5, 0, .5, 1}) {
		t.Fatal(nodes)
	}
}
func TestStatusExclusion(t *testing.T) {
	for _, s := range []string{"rex-status", "rex-statusline:block:abc"} {
		if !IsStatus(Block{Label: s}) {
			t.Fatal(s)
		}
	}
	if IsStatus(Block{Label: "shell"}) {
		t.Fatal("shell excluded")
	}
}
