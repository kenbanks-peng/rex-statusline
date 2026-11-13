package render

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestBuiltInConfigMatchesExample(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	got, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	want, err := Load("../defaults/config.toml")
	if err != nil {
		t.Fatal(err)
	}
	if got.Placement.Top || got.Placement.Horizontal != "center" || got.Placement.Manual {
		t.Fatalf("default placement is not bottom center: %+v", got.Placement)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("built-in configuration differs from example")
	}
}

func TestUnreadableConfigDoesNotFallBack(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	path := filepath.Join(base, "rex-statusline", "config.toml")
	// A directory reliably fails to read even when tests run as root.
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	_, err := Load("")
	if err == nil || !strings.Contains(err.Error(), "cannot read configuration:\n  "+path) {
		t.Fatalf("expected readable configuration error, got %v", err)
	}
}

func TestConfiguredTheme(t *testing.T) {
	for _, absolute := range []bool{false, true} {
		name := "relative"
		if absolute {
			name = "absolute"
		}
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", base)
			path := filepath.Join(base, "rex-statusline", "config.toml")
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			themePath := filepath.Join(t.TempDir(), "custom.toml")
			if !absolute {
				themePath = filepath.Join(filepath.Dir(path), "themes", "custom.toml")
				if err := os.MkdirAll(filepath.Dir(themePath), 0700); err != nil {
					t.Fatal(err)
				}
			}
			setting := themePath
			if !absolute {
				setting = "themes/custom.toml"
			}
			if err := os.WriteFile(path, []byte("[renderer]\ntheme = '"+setting+"'\n"), 0600); err != nil {
				t.Fatal(err)
			}
			for _, configPath := range []string{"", path} {
				if _, err := Load(configPath); err == nil || !strings.Contains(err.Error(), themePath) {
					t.Fatalf("missing custom theme did not fail: %v", err)
				}
			}
			if err := os.WriteFile(themePath, []byte("version = 4\ntext = 'custom'\n"), 0600); err != nil {
				t.Fatal(err)
			}
			for _, configPath := range []string{"", path} {
				cfg, err := Load(configPath)
				if err != nil || cfg.Theme["text"] != "custom" {
					t.Fatalf("configured theme: %+v, %v", cfg, err)
				}
			}
		})
	}
}

func TestSeparateConfigFiles(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	dir := filepath.Join(base, "rex-statusline")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.toml")
	themePath := filepath.Join(dir, "oh-my-posh.toml")
	write := func(path, data string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(themePath, "version = 4\ntext = 'custom'\n")
	cfg, err := Load("")
	if err != nil || cfg.Theme["text"] != "custom" || cfg.Executable != "oh-my-posh" {
		t.Fatalf("theme-only override: %+v, %v", cfg, err)
	}
	write(path, "[renderer]\ntimeout = '2s'\n")
	cfg, err = Load(path)
	if err != nil || cfg.Theme["text"] != "custom" {
		t.Fatalf("explicit pair: %+v, %v", cfg, err)
	}
	for _, data := range []string{"", "[broken"} {
		write(themePath, data)
		if _, err := Load(""); err == nil || !strings.Contains(err.Error(), themePath) {
			t.Fatalf("accepted invalid theme %q: %v", data, err)
		}
	}
	if err := os.Remove(themePath); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), themePath) {
		t.Fatalf("missing explicit theme: %v", err)
	}
	if cfg, err := Load(""); err != nil || len(cfg.Theme) == 0 {
		t.Fatalf("settings-only override: %+v, %v", cfg, err)
	}
	if err := os.Mkdir(themePath, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(""); err == nil || !strings.Contains(err.Error(), "cannot read configuration") {
		t.Fatalf("unreadable theme: %v", err)
	}
}
