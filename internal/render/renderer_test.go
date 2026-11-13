package render

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--version":
			switch filepath.Base(os.Args[0]) {
			case "node":
				fmt.Println("v24.1.0")
			case "deno":
				fmt.Println("deno 2.5.0")
			default:
				fmt.Println("Python 3.14.0")
			}
			syscall.Exit(0)
		case "linger":
			time.Sleep(10 * time.Second)
			syscall.Exit(0)
		case "version":
			if strings.Contains(os.Args[0], "slow-version") {
				time.Sleep(10 * time.Second)
			}
			if strings.Contains(os.Args[0], "failed-version") {
				syscall.Exit(2)
			}
			version := "32.0.0"
			for _, candidate := range []string{"31.0.0", "33.0.0", "99.0.0", "v33.0.0-beta.1"} {
				if strings.HasPrefix(filepath.Base(os.Args[0]), candidate) {
					version = candidate
					break
				}
			}
			fmt.Print(version)
			syscall.Exit(0)
		case "print":
			switch os.Getenv("FAKE_MODE") {
			case "filesystem":
				data, err := os.ReadFile(os.Getenv("THEME_STATE_FILE"))
				if err != nil {
					syscall.Exit(2)
				}
				fmt.Print(string(data) + "|" + os.Getenv("BSP_USER_ID"))
			case "children":
				child := exec.Command(os.Args[0], "linger")
				if child.Start() != nil {
					syscall.Exit(2)
				}
				if os.WriteFile(os.Getenv("CHILD_PID_FILE"), []byte(strconv.Itoa(child.Process.Pid)), 0600) != nil {
					syscall.Exit(2)
				}
				time.Sleep(10 * time.Second)
			case "sleep":
				time.Sleep(10 * time.Second)
			case "large":
				fmt.Print(strings.Repeat("x", 100000))
			default:
				fmt.Print(os.Getenv("DIRPATH") + "|" + os.Getenv("JOBCOUNT") + "|" + os.Getenv("VIRTUAL_ENV") + "|" + os.Getenv("MANAGER_ONLY") + "|" + strings.Join(os.Args[2:], " "))
			}
			syscall.Exit(0)
		}
	}
	os.Exit(m.Run())
}

func fakeOMP(t *testing.T, name string) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), name+" executable")
	if err := os.WriteFile(path, data, 0700); err != nil {
		t.Fatal(err)
	}
	return path
}
func fakeConfig(t *testing.T) Config {
	return Config{Executable: fakeOMP(t, "omp"), Timeout: time.Second, Theme: map[string]any{"version": 4}}
}
func TestRendererLifecycle(t *testing.T) {
	t.Setenv("MANAGER_ONLY", "do-not-inherit")
	t.Setenv("VIRTUAL_ENV", "manager-venv")
	cfg := fakeConfig(t)
	r, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	entries, _ := os.ReadDir(r.dir)
	if len(entries) != 2 {
		t.Fatalf("theme files: %v", entries)
	}
	for path, want := range map[string]os.FileMode{r.dir: 0700, r.theme: 0600, r.unknownTheme: 0600} {
		st, err := os.Stat(path)
		if err != nil || st.Mode().Perm() != want {
			t.Fatalf("permissions %s: %v %v", path, st, err)
		}
	}
	data, _ := os.ReadFile(r.theme)
	var theme map[string]any
	if json.Unmarshal(data, &theme) != nil || theme["version"] != float64(4) {
		t.Fatalf("theme %s", data)
	}
	in := Input{ShellKnown: true, Cwd: t.TempDir(), Jobs: 3, ExitCode: 7, DurationMS: 1234, Columns: 1000}
	out, err := r.Render(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{" 3", "--execution-time 1234", "--status 7", "--shell uni", "--escape=false"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q: %s", want, out)
		}
	}
	if strings.Contains(out, "manager-venv") || strings.Contains(out, "do-not-inherit") {
		t.Fatal("manager environment leaked")
	}
	dir := r.dir
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("theme not cleaned")
	}
	if _, err := r.Render(context.Background(), in); err == nil {
		t.Fatal("render after close")
	}
}
func TestRendererAcceptsDifferentVersions(t *testing.T) {
	for _, version := range []string{"31.0.0", "32.0.0", "33.0.0", "99.0.0", "v33.0.0-beta.1"} {
		t.Run(version, func(t *testing.T) {
			cfg := fakeConfig(t)
			cfg.Executable = fakeOMP(t, version)
			r, err := New(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			if _, err := r.Render(context.Background(), Input{Cwd: t.TempDir(), Columns: 80}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestExecutableValidation(t *testing.T) {
	for _, name := range []string{"failed-version", "slow-version"} {
		cfg := fakeConfig(t)
		cfg.Executable = fakeOMP(t, name)
		cfg.Timeout = 50 * time.Millisecond
		if _, err := New(context.Background(), cfg); err == nil {
			t.Fatalf("accepted %s", name)
		}
	}
	cfg := fakeConfig(t)
	cfg.Executable = "/does/not/exist"
	if _, err := New(context.Background(), cfg); err == nil {
		t.Fatal("accepted missing executable")
	}
	cfg = fakeConfig(t)
	t.Setenv("PATH", filepath.Dir(cfg.Executable))
	cfg.Executable = filepath.Base(cfg.Executable)
	r, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
}
func TestTimeoutAndBoundedOutput(t *testing.T) {
	for _, mode := range []string{"sleep", "large"} {
		t.Run(mode, func(t *testing.T) {
			cfg := fakeConfig(t)
			cfg.Timeout = time.Second
			cfg.MaxOutputBytes = 1024
			r, err := New(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			r.cfg.Timeout = 200 * time.Millisecond
			start := time.Now()
			_, err = r.Render(context.Background(), Input{Cwd: t.TempDir(), Env: map[string]string{"FAKE_MODE": mode}, Columns: 80})
			if err == nil || time.Since(start) > time.Second {
				t.Fatalf("unbounded render: %v", err)
			}
		})
	}
	cfg := fakeConfig(t)
	r, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Render(ctx, Input{Cwd: t.TempDir(), Columns: 80}); err == nil {
		t.Fatal("ignored cancellation")
	}
}
func TestLoad(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	expected := filepath.Join(dir, "rex-statusline", "config.toml")
	if cfg, err := Load(""); err != nil || len(cfg.Theme) == 0 {
		t.Fatalf("missing built-in default: %+v %v", cfg, err)
	}
	if _, err := os.Stat(expected); !os.IsNotExist(err) {
		t.Fatalf("default loading wrote configuration: %v", err)
	}
	if _, err := Load(expected); err == nil || !strings.Contains(err.Error(), "configuration file not found:\n  "+expected) || !strings.Contains(err.Error(), "omit it") {
		t.Fatalf("missing actionable path: %v", err)
	}
	os.MkdirAll(filepath.Dir(expected), 0700)
	os.WriteFile(expected, []byte("[renderer]\ntimeout='1s'\n"), 0600)
	cfg, err := Load("")
	if err != nil || cfg.Timeout != time.Second {
		t.Fatalf("%+v %v", cfg, err)
	}
	for _, data := range []string{"[renderer]\ntimeout='-1s'", "[renderer]\nunknown = true", "invalid = ["} {
		os.WriteFile(expected, []byte(data), 0600)
		if _, err := Load(""); err == nil {
			t.Fatalf("accepted %q", data)
		}
	}
}
func TestExampleWithInstalledOMP(t *testing.T) {
	if _, err := exec.LookPath("oh-my-posh"); err != nil {
		t.Skip("OMP not installed")
	}
	cfg, err := Load("../defaults/config.toml")
	if err != nil {
		t.Fatal(err)
	}
	r, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	out, err := r.Render(context.Background(), Input{ShellKnown: true, Cwd: t.TempDir(), Columns: 120, ExitCode: 9, DurationMS: 1500})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ansi.Strip(out), "1.5") {
		t.Fatalf("duration missing: %q", out)
	}
	if ansi.StringWidth(out) > 120 {
		t.Fatal("exceeds width")
	}
}

func TestCancellationKillsChildren(t *testing.T) {
	cfg := fakeConfig(t)
	r, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err = r.Render(ctx, Input{Cwd: t.TempDir(), Columns: 80, Env: map[string]string{"FAKE_MODE": "children", "CHILD_PID_FILE": pidFile}})
	if err == nil {
		t.Fatal("expected cancellation")
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("child not started: %v", err)
	}
	pid, err := strconv.Atoi(string(data))
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Kill(pid, syscall.SIGKILL)
	for until := time.Now().Add(time.Second); time.Now().Before(until); {
		if syscall.Kill(pid, 0) == syscall.ESRCH {
			return
		}
		// A terminated, unreaped orphan may briefly remain a zombie.
		out, _ := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
		if strings.HasPrefix(strings.TrimSpace(string(out)), "Z") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("descendant %d survived cancellation", pid)
}
