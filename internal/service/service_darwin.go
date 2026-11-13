package service

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const label = "local.rex-statusline"

func Ensure(ctx context.Context, args []string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	executable, err = launchAgentExecutable(executable)
	if err != nil {
		return err
	}
	directory := filepath.Join(home, "Library", "LaunchAgents")
	logDirectory := filepath.Join(home, "Library", "Logs", "rex-statusline")
	for _, path := range []string{directory, logDirectory} {
		if err := os.MkdirAll(path, 0700); err != nil {
			return err
		}
	}
	var plist bytes.Buffer
	plist.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n<plist version=\"1.0\"><dict>\n")
	plist.WriteString("<key>Label</key>")
	stringValue(&plist, label)
	plist.WriteString("<key>ProgramArguments</key><array>")
	for _, arg := range append([]string{executable}, args...) {
		stringValue(&plist, arg)
	}
	plist.WriteString("</array><key>EnvironmentVariables</key><dict>")
	for _, name := range []string{"HOME", "PATH", "LANG", "LC_ALL", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "REX_SERVER"} {
		if value, ok := os.LookupEnv(name); ok {
			plist.WriteString("<key>" + name + "</key>")
			stringValue(&plist, value)
		}
	}
	plist.WriteString("</dict><key>WorkingDirectory</key>")
	stringValue(&plist, home)
	plist.WriteString("<key>StandardOutPath</key>")
	stringValue(&plist, filepath.Join(logDirectory, "manager.log"))
	plist.WriteString("<key>StandardErrorPath</key>")
	stringValue(&plist, filepath.Join(logDirectory, "manager.log"))
	plist.WriteString("<key>RunAtLoad</key><true/><key>KeepAlive</key><true/><key>ThrottleInterval</key><integer>10</integer>\n</dict></plist>\n")

	return ensureLaunchAgent(ctx, filepath.Join(directory, label+".plist"), plist.Bytes(), launchctl)
}

func ensureLaunchAgent(ctx context.Context, path string, plist []byte, run func(context.Context, ...string) ([]byte, error)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	current, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	domain := "gui/" + strconv.Itoa(os.Getuid())
	target := domain + "/" + label
	status, loadedErr := run(ctx, "print", target)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	changed := !bytes.Equal(current, plist)
	if loadedErr != nil {
		var exit *exec.ExitError
		if !errors.As(loadedErr, &exit) || exit.ExitCode() != 113 {
			return loadedErr
		}
	}
	if loadedErr == nil && !changed {
		for _, line := range strings.Split(string(status), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 3 && fields[0] == "pid" && fields[1] == "=" {
				if pid, err := strconv.Atoi(fields[2]); err == nil && pid > 0 {
					return nil
				}
			}
		}
		_, err := run(ctx, "kickstart", target)
		return err
	}
	if loadedErr == nil {
		if _, err := run(ctx, "bootout", target); err != nil {
			return err
		}
	}
	if changed {
		if err := writePlist(path, plist); err != nil {
			return err
		}
	}
	if err := bootstrap(ctx, domain, path, run); err != nil {
		return err
	}
	return nil
}

// Prefer Homebrew's upgrade-stable alias only when it identifies this binary.
func launchAgentExecutable(executable string) (string, error) {
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return "", err
	}
	bin := filepath.Dir(resolved)
	version := filepath.Dir(bin)
	formula := filepath.Dir(version)
	cellar := filepath.Dir(formula)
	if filepath.Base(cellar) != "Cellar" || filepath.Base(formula) != "rex-statusline" || filepath.Base(bin) != "bin" {
		return executable, nil
	}
	stable := filepath.Join(filepath.Dir(cellar), "opt", "rex-statusline", "bin", filepath.Base(resolved))
	current, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if candidate, err := os.Stat(stable); err == nil && os.SameFile(current, candidate) {
		return stable, nil
	}
	return executable, nil
}

func bootstrap(ctx context.Context, domain, path string, run func(context.Context, ...string) ([]byte, error)) error {
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		_, err := run(ctx, "bootstrap", domain, path)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var exit *exec.ExitError
		if err == nil || !errors.As(err, &exit) || exit.ExitCode() != 5 || attempt == 20 {
			return err
		}
		// bootout can return before launchd finishes removing the old service.
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func Stop(ctx context.Context) error {
	return stop(ctx, launchctl)
}

func stop(ctx context.Context, run func(context.Context, ...string) ([]byte, error)) error {
	target := "gui/" + strconv.Itoa(os.Getuid()) + "/" + label
	_, err := run(ctx, "bootout", target)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var exit *exec.ExitError
	// launchctl returns ESRCH when the agent is already unloaded.
	if errors.As(err, &exit) && exit.ExitCode() == 3 {
		return nil
	}
	return err
}

func stringValue(out *bytes.Buffer, value string) {
	out.WriteString("<string>")
	xml.EscapeText(out, []byte(value))
	out.WriteString("</string>")
}

func writePlist(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".rex-statusline-*.plist")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func launchctl(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/bin/launchctl", args...).CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("launchctl %v: %w: %s", args, err, bytes.TrimSpace(out))
	}
	return out, nil
}
