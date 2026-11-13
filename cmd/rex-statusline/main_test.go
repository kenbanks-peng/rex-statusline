package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"rex-statusline/internal/defaults"
	"rex-statusline/internal/render"
	"strings"
	"testing"
)

func TestNoArgumentsShowsUsage(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	stdout := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = stdout }()

	runErr := run(context.Background(), nil)
	w.Close()
	output, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if runErr != nil {
		t.Fatal(runErr)
	}
	for _, want := range []string{"Usage: rex-statusline", "add", "remove"} {
		if !strings.Contains(string(output), want) {
			t.Fatalf("usage missing %q: %s", want, output)
		}
	}
}

func TestAddArguments(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "help", args: []string{"add", "--help"}},
		{name: "positional", args: []string{"add", "extra"}, want: "unexpected positional arguments"},
		{name: "unknown flag", args: []string{"add", "--unknown"}, want: "flag provided but not defined"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := run(context.Background(), tc.args)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}
}

func TestRemoveArguments(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "help", args: []string{"remove", "--help"}},
		{name: "positional", args: []string{"remove", "extra"}, want: "usage: rex-statusline remove"},
		{name: "unknown flag", args: []string{"remove", "--unknown"}, want: "flag provided but not defined"},
		{name: "manage flag", args: []string{"remove", "--session", "test"}, want: "flag provided but not defined"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := run(context.Background(), tc.args)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}
}

func TestConfigInit(t *testing.T) {
	for _, useXDG := range []bool{true, false} {
		name := "home"
		if useXDG {
			name = "xdg"
		}
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			t.Setenv("HOME", base)
			t.Setenv("XDG_CONFIG_HOME", "")
			configBase := filepath.Join(base, ".config")
			if useXDG {
				configBase = filepath.Join(base, "custom")
				t.Setenv("XDG_CONFIG_HOME", configBase)
			}
			wantConfig, err := render.Load("")
			if err != nil {
				t.Fatal(err)
			}
			if err := run(context.Background(), []string{"config", "init"}); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(configBase, "rex-statusline", "config.toml")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(data, defaults.Config) {
				t.Fatal("generated configuration differs from defaults")
			}
			for _, key := range []string{"timeout", "max_output_bytes"} {
				if bytes.Contains(data, []byte(key)) {
					t.Fatalf("generated configuration includes %s", key)
				}
			}
			theme, err := os.ReadFile(filepath.Join(filepath.Dir(path), "oh-my-posh.toml"))
			if err != nil || !bytes.Equal(theme, defaults.Theme) {
				t.Fatalf("generated theme differs from defaults: %v", err)
			}
			gotConfig, err := render.Load("")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(gotConfig, wantConfig) {
				t.Fatal("generated configuration changes defaults")
			}
			if err := os.WriteFile(path, []byte("keep me"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := initConfig(context.Background(), strings.NewReader("no\n"), io.Discard); err != nil {
				t.Fatal(err)
			}
			data, err = os.ReadFile(path)
			if err != nil || string(data) != "keep me" {
				t.Fatalf("existing config changed: %q, %v", data, err)
			}
		})
	}
}

func TestConfigArguments(t *testing.T) {
	for _, args := range [][]string{
		{"config"}, {"config", "unknown"}, {"config", "init", "extra"},
		{"config", "init", "--unknown"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			base := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", base)
			if err := run(context.Background(), args); err == nil {
				t.Fatal("expected error")
			}
			entries, err := os.ReadDir(base)
			if err != nil || len(entries) != 0 {
				t.Fatalf("unexpected files: %v, %v", entries, err)
			}
		})
	}
	for _, args := range [][]string{{"config", "--help"}, {"config", "init", "--help"}} {
		if err := run(context.Background(), args); err != nil {
			t.Fatal(err)
		}
	}
}

func TestConfigInitRejectsRelativeXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "relative")
	if err := run(context.Background(), []string{"config", "init"}); err == nil || !strings.Contains(err.Error(), "must be absolute") {
		t.Fatalf("got %v, want invalid XDG path error", err)
	}
}

func TestConfigInitParentIsFile(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	if err := os.WriteFile(filepath.Join(base, "rex-statusline"), []byte("keep me"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"config", "init"}); err == nil {
		t.Fatal("expected filesystem error")
	}
}

func TestConfigInitPreservesExistingTheme(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	dir := filepath.Join(base, "rex-statusline")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "oh-my-posh.toml")
	if err := os.WriteFile(path, []byte("keep theme"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := initConfig(context.Background(), strings.NewReader(""), io.Discard); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "keep theme" {
		t.Fatalf("theme changed: %q, %v", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "config.toml")); err != nil || !bytes.Equal(data, defaults.Config) {
		t.Fatalf("missing config not created: %q, %v", data, err)
	}
}

func TestConfigInitIndependentChoices(t *testing.T) {
	for _, answers := range []string{"y\nn\n", "n\ny\n"} {
		t.Run(answers, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			path, _ := render.DefaultConfigPath()
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			paths := []string{path, filepath.Join(filepath.Dir(path), "oh-my-posh.toml")}
			defaults := [][]byte{defaults.Config, defaults.Theme}
			for _, path := range paths {
				if err := os.WriteFile(path, []byte("custom"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			var output bytes.Buffer
			if err := initConfig(context.Background(), strings.NewReader(answers), &output); err != nil {
				t.Fatal(err)
			}
			if strings.Count(output.String(), "with defaults? [y/N]") != 2 {
				t.Fatalf("expected two prompts: %s", &output)
			}
			for i, path := range paths {
				want := []byte("custom")
				if strings.Split(answers, "\n")[i] == "y" {
					want = defaults[i]
				}
				got, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(got, want) {
					t.Fatalf("%s: got %q, %v", path, got, err)
				}
			}
		})
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

func TestConfigInitCancellation(t *testing.T) {
	for _, both := range []bool{false, true} {
		t.Run(fmt.Sprint(both), func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			path, _ := render.DefaultConfigPath()
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			theme := filepath.Join(filepath.Dir(path), "oh-my-posh.toml")
			paths := []string{theme}
			if both {
				paths = append(paths, path)
			}
			for _, path := range paths {
				if err := os.WriteFile(path, []byte("custom"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			reader, writer := io.Pipe()
			defer reader.Close()
			defer writer.Close()
			output := writerFunc(func(p []byte) (int, error) {
				if strings.Contains(string(p), theme) {
					cancel()
				}
				return len(p), nil
			})
			if err := initConfig(ctx, io.MultiReader(strings.NewReader("y\n"), reader), output); !errors.Is(err, context.Canceled) {
				t.Fatalf("expected cancellation, got %v", err)
			}
			for _, path := range paths {
				got, err := os.ReadFile(path)
				if err != nil || string(got) != "custom" {
					t.Fatalf("file changed on cancellation: %q, %v", got, err)
				}
			}
			if !both {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("missing config created on cancellation: %v", err)
				}
			}
		})
	}
}

func TestConfigInitEscape(t *testing.T) {
	for _, input := range []string{"\x1b", "y\n\x1b", "y\n\x03"} {
		t.Run(input, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			path, _ := render.DefaultConfigPath()
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			paths := []string{path, filepath.Join(filepath.Dir(path), "oh-my-posh.toml")}
			for _, path := range paths {
				if err := os.WriteFile(path, []byte("custom"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := initConfig(context.Background(), strings.NewReader(input), io.Discard); !errors.Is(err, context.Canceled) {
				t.Fatalf("expected cancellation, got %v", err)
			}
			for _, path := range paths {
				got, err := os.ReadFile(path)
				if err != nil || string(got) != "custom" {
					t.Fatalf("file changed on escape: %q, %v", got, err)
				}
			}
		})
	}
}

func TestConfigInitReplacement(t *testing.T) {
	for _, existing := range []string{"config.toml", "oh-my-posh.toml", "both"} {
		for _, answer := range []string{"y\n", " YES \n", "n\n", "\n", "", "maybe\n"} {
			t.Run(existing+"/"+answer, func(t *testing.T) {
				t.Setenv("XDG_CONFIG_HOME", t.TempDir())
				path, err := render.DefaultConfigPath()
				if err != nil {
					t.Fatal(err)
				}
				dir := filepath.Dir(path)
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
				files := map[string][]byte{"config.toml": defaults.Config, "oh-my-posh.toml": defaults.Theme}
				for name := range files {
					if existing == name || existing == "both" {
						if err := os.WriteFile(filepath.Join(dir, name), []byte("custom"), 0600); err != nil {
							t.Fatal(err)
						}
					}
				}
				var output bytes.Buffer
				if err := initConfig(context.Background(), strings.NewReader(answer+answer), &output); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(output.String(), "with defaults? [y/N]") {
					t.Fatalf("missing confirmation: %s", &output)
				}
				confirmed := strings.EqualFold(strings.TrimSpace(answer), "y") || strings.EqualFold(strings.TrimSpace(answer), "yes")
				for name, defaults := range files {
					data, err := os.ReadFile(filepath.Join(dir, name))
					if confirmed || (existing != name && existing != "both") {
						if err != nil || !bytes.Equal(data, defaults) {
							t.Fatalf("%s not reset to defaults: %q, %v", name, data, err)
						}
					} else if existing == name || existing == "both" {
						if err != nil || string(data) != "custom" {
							t.Fatalf("%s changed without confirmation: %q, %v", name, data, err)
						}
					} else if !os.IsNotExist(err) {
						t.Fatalf("%s created without confirmation: %v", name, err)
					}
				}
			})
		}
	}
}
