package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func systemdTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	config, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(config, "systemd", "user", systemdUnit)
}

func TestEnsureSystemd(t *testing.T) {
	path := systemdTestHome(t)
	t.Setenv("REX_SERVER", "host %h $HOME \"quoted\"\nnext")
	t.Setenv("PATH", "/custom/bin:/usr/bin")
	var calls [][]string
	run := func(_ context.Context, args ...string) error {
		calls = append(calls, args)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("unit must exist before systemctl: %v", err)
		}
		return nil
	}
	check := func(config string) {
		t.Helper()
		calls = nil
		if err := ensureSystemd(context.Background(), []string{"manage", "--foreground", "--config", config}, run); err != nil {
			t.Fatal(err)
		}
		want := [][]string{{"daemon-reload"}, {"enable", systemdUnit}, {"restart", systemdUnit}}
		if !reflect.DeepEqual(calls, want) {
			t.Fatalf("calls = %v, want %v", calls, want)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		executable, err = filepath.EvalSymlinks(executable)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range []string{
			"ExecStart=" + systemdQuote(strings.ReplaceAll(executable, "$", "$$")) + " \"manage\" \"--foreground\" \"--config\" " + systemdQuote(strings.ReplaceAll(config, "$", "$$")),
			"WorkingDirectory=" + systemdQuote(os.Getenv("HOME")),
			"Environment=\"PATH=/custom/bin:/usr/bin\"",
			"Environment=\"REX_SERVER=host %%h $HOME \\\"quoted\\\"\\nnext\"",
			"Restart=always",
			"RestartSec=10",
			"WantedBy=default.target",
		} {
			if !strings.Contains(string(data), line+"\n") {
				t.Errorf("unit missing %q:\n%s", line, data)
			}
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("unit permissions: %v, %v", info, err)
		}
	}
	check("/config with spaces/%h/$HOME/\"quote\"\\path\nfile")
	check("/updated/config.toml")
}

func TestEnsureSystemdFailures(t *testing.T) {
	for _, failAt := range []int{0, 1, 2} {
		t.Run([]string{"reload", "enable", "restart"}[failAt], func(t *testing.T) {
			systemdTestHome(t)
			failure := errors.New("systemctl failed")
			calls := 0
			err := ensureSystemd(context.Background(), nil, func(context.Context, ...string) error {
				defer func() { calls++ }()
				if calls == failAt {
					return failure
				}
				return nil
			})
			if !errors.Is(err, failure) || calls != failAt+1 {
				t.Fatalf("got %v after %d calls", err, calls)
			}
		})
	}
}

func TestEnsureSystemdCanceled(t *testing.T) {
	path := systemdTestHome(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := ensureSystemd(ctx, nil, func(context.Context, ...string) error {
		t.Fatal("called systemctl after cancellation")
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("created unit after cancellation: %v", err)
	}
}

func TestSystemdQuote(t *testing.T) {
	got := systemdQuote("a\\b\"c\nd\re\tf%h $HOME")
	want := `"a\\b\"c\nd\re\tf%%h $HOME"`
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestSystemctl(t *testing.T) {
	directory := t.TempDir()
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$CALLS\"\necho unavailable >&2\nexit \"$RESULT\"\n"
	if err := os.WriteFile(filepath.Join(directory, "systemctl"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "calls")
	t.Setenv("PATH", directory)
	t.Setenv("CALLS", path)
	t.Setenv("RESULT", "0")
	if err := systemctl(context.Background(), "stop", systemdUnit); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "--user\nstop\nrex-statusline.service\n" {
		t.Fatalf("systemctl arguments = %q, %v", data, err)
	}
	t.Setenv("RESULT", "1")
	err = systemctl(context.Background(), "daemon-reload")
	if err == nil || !strings.Contains(err.Error(), "unavailable") || !strings.Contains(err.Error(), "manage --foreground") {
		t.Fatalf("missing actionable failure: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := systemctl(ctx, "stop", systemdUnit); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}
