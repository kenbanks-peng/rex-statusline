// Package render renders native block context through Oh My Posh.
package render

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/pelletier/go-toml/v2"
	"rex-statusline/internal/defaults"
	"rex-statusline/internal/placement"
)

type Config struct {
	Placement      placement.Anchor
	Executable     string
	Timeout        time.Duration
	MaxOutputBytes int
	Theme          map[string]any
}

func Load(path string) (Config, error) {
	explicit := path != ""
	if path == "" {
		var err error
		path, err = DefaultConfigPath()
		if err != nil {
			return Config{}, err
		}
	}
	data, err := readConfigFile(path, defaults.Config, !explicit)
	if err != nil {
		return Config{}, err
	}
	var file struct {
		Window struct {
			Placement string `toml:"placement"`
		} `toml:"window"`
		Renderer struct {
			Executable     string `toml:"executable"`
			Theme          string `toml:"theme"`
			Timeout        string `toml:"timeout"`
			MaxOutputBytes int    `toml:"max_output_bytes"`
		} `toml:"renderer"`
	}
	if err := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields().Decode(&file); err != nil {
		return Config{}, fmt.Errorf("invalid configuration:\n  %s\n%w", path, err)
	}
	placement, err := placement.Parse(file.Window.Placement)
	if err != nil {
		return Config{}, fmt.Errorf("%s: window.placement: %w", path, err)
	}
	cfg := Config{Placement: placement, Executable: file.Renderer.Executable, MaxOutputBytes: file.Renderer.MaxOutputBytes}
	if file.Renderer.Timeout != "" {
		cfg.Timeout, err = time.ParseDuration(file.Renderer.Timeout)
		if err != nil || cfg.Timeout <= 0 {
			return Config{}, fmt.Errorf("%s: renderer.timeout must be a positive duration", path)
		}
	}
	if cfg.MaxOutputBytes < 0 {
		return Config{}, fmt.Errorf("%s: renderer.max_output_bytes must be positive", path)
	}
	themePath := file.Renderer.Theme
	if themePath == "" {
		themePath = "oh-my-posh.toml"
	}
	allowMissingTheme := !explicit && themePath == "oh-my-posh.toml"
	if !filepath.IsAbs(themePath) {
		themePath = filepath.Join(filepath.Dir(path), themePath)
	}
	theme, err := readConfigFile(themePath, defaults.Theme, allowMissingTheme)
	if err != nil {
		return Config{}, err
	}
	if err := toml.Unmarshal(theme, &cfg.Theme); err != nil {
		return Config{}, fmt.Errorf("invalid theme:\n  %s\n%w", themePath, err)
	}
	if len(cfg.Theme) == 0 {
		return Config{}, fmt.Errorf("%s: empty Oh My Posh theme", themePath)
	}
	return cfg, nil
}

func readConfigFile(path string, fallback []byte, allowMissing bool) ([]byte, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if allowMissing {
			return fallback, nil
		}
		return nil, fmt.Errorf("configuration file not found:\n  %s\nCheck --config and renderer.theme, or omit it to use the default configuration.", path)
	}
	if err != nil {
		var pathErr *os.PathError
		if errors.As(err, &pathErr) {
			err = pathErr.Err
		}
		return nil, fmt.Errorf("cannot read configuration:\n  %s\n%w", path, err)
	}
	return data, nil
}

// DefaultConfigPath returns the configuration path used when --config is omitted.
func DefaultConfigPath() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	if !filepath.IsAbs(base) {
		return "", fmt.Errorf("XDG_CONFIG_HOME must be absolute")
	}
	return filepath.Join(base, "rex-statusline", "config.toml"), nil
}
