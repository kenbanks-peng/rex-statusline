package render

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestPlacementConfigReload(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "config.toml")
	executable := fakeOMP(t, "omp")
	writeWatchConfig(t, path, executable, "status")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	r, err := New(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if got := r.Placement(); got.Horizontal != "" || got.Top || got.Manual {
		t.Fatalf("default placement %v", got)
	}
	for _, tc := range []struct {
		placement string
		want      string
		valid     bool
	}{
		{"top center", "center", true}, {"right bottom", "right", true},
		{"top bottom", "right", false}, {"left right", "right", false},
		{"bogus", "right", false}, {"manual", "", true},
	} {
		data := fmt.Sprintf("[renderer]\nexecutable = %q\n[window]\nplacement = %q\n", executable, tc.placement)
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		err := r.reload(ctx, path)
		if (err == nil) != tc.valid {
			t.Fatalf("placement %s: error %v", tc.placement, err)
		}
		if got := r.Placement(); got.Horizontal != tc.want {
			t.Fatalf("placement %s: got %v want %v", tc.placement, got, tc.want)
		}
	}
}

func TestWidthConfigurationRemoved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[window]\nwidth = '50%'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("accepted removed width setting")
	}
}
