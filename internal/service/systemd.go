package service

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const systemdUnit = "rex-statusline.service"

type systemctlRunner func(context.Context, ...string) error

func ensureSystemd(ctx context.Context, args []string, run systemctlRunner) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	config, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	if !filepath.IsAbs(config) {
		return fmt.Errorf("systemd user configuration directory must be absolute: %q", config)
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return err
	}
	var unit strings.Builder
	unit.WriteString("[Unit]\nDescription=Rex status lines\n\n[Service]\nType=simple\nExecStart=")
	for i, arg := range append([]string{executable}, args...) {
		if i > 0 {
			unit.WriteByte(' ')
		}
		unit.WriteString(systemdQuote(strings.ReplaceAll(arg, "$", "$$")))
	}
	unit.WriteString("\nWorkingDirectory=" + systemdQuote(home) + "\n")
	for _, name := range []string{"HOME", "PATH", "LANG", "LC_ALL", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "REX_SERVER"} {
		if value, ok := os.LookupEnv(name); ok {
			unit.WriteString("Environment=" + systemdQuote(name+"="+value) + "\n")
		}
	}
	unit.WriteString("Restart=always\nRestartSec=10\n\n[Install]\nWantedBy=default.target\n")
	directory := filepath.Join(config, "systemd", "user")
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(directory, ".rex-statusline-*.service")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.WriteString(unit.String()); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), filepath.Join(directory, systemdUnit)); err != nil {
		return err
	}
	for _, command := range [][]string{{"daemon-reload"}, {"enable", systemdUnit}, {"restart", systemdUnit}} {
		if err := run(ctx, command...); err != nil {
			return err
		}
	}
	return nil
}

func systemdQuote(value string) string {
	return `"` + strings.NewReplacer("\\", "\\\\", `"`, `\"`, "\n", `\n`, "\r", `\r`, "\t", `\t`, "%", "%%").Replace(value) + `"`
}

func systemctl(ctx context.Context, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	args = append([]string{"--user"}, args...)
	out, err := exec.CommandContext(ctx, "systemctl", args...).CombinedOutput()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return fmt.Errorf("systemctl %v: %w: %s (requires a running systemd user manager; otherwise use manage --foreground)", args, err, strings.TrimSpace(string(out)))
	}
	return nil
}
