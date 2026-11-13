package render

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"rex-statusline/internal/placement"
	"rex-statusline/internal/timing"
)

type Input struct {
	// ShellKnown distinguishes measured zero from unavailable shell state.
	ShellKnown bool
	UserID     *int
	Cwd        string
	Env        map[string]string
	Jobs       int
	ExitCode   int
	DurationMS int64
	Columns    int
}

type Renderer struct {
	mu                                   sync.RWMutex
	cfg                                  Config
	executable, dir, theme, unknownTheme string
	closed                               bool
	dependencies                         nativeDependencies
	cacheable                            bool
	cacheMu                              sync.Mutex
	cache                                map[string]cachedRender
	hostMu                               sync.Mutex
	hosts                                map[[32]byte]*hostnameLookup
}

func withDefaults(cfg Config) Config {
	if cfg.Executable == "" {
		cfg.Executable = "oh-my-posh"
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 3 * time.Second
	}
	if cfg.MaxOutputBytes == 0 {
		cfg.MaxOutputBytes = 256 * 1024
	}
	return cfg
}

func New(ctx context.Context, cfg Config) (*Renderer, error) {
	cfg = withDefaults(cfg)
	if cfg.Timeout < 0 || cfg.MaxOutputBytes < 1 {
		return nil, errors.New("invalid renderer limits")
	}
	if len(cfg.Theme) == 0 {
		return nil, errors.New("missing Oh My Posh theme")
	}
	executable, err := exec.LookPath(cfg.Executable)
	if err != nil {
		return nil, fmt.Errorf("Oh My Posh executable %q: install Oh My Posh 32 or set renderer.executable: %w", cfg.Executable, err)
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return nil, err
	}
	r := &Renderer{cfg: cfg, executable: executable}
	r.dependencies, r.cacheable = themeDependencies(cfg.Theme)
	check, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	version, err := r.run(check, executable, []string{"version"}, "", baseEnv(nil), 4096)
	if err != nil {
		return nil, fmt.Errorf("Oh My Posh version check: %w", err)
	}
	if !regexp.MustCompile(`^v?32\.\d+\.\d+(?:[-+][^\s]+)?$`).MatchString(strings.TrimSpace(version)) {
		return nil, fmt.Errorf("unsupported Oh My Posh version %q; supported major: 32", strings.TrimSpace(version))
	}
	data, err := json.Marshal(cfg.Theme)
	if err != nil {
		return nil, fmt.Errorf("encode theme: %w", err)
	}
	r.dir, err = os.MkdirTemp("", "rex-statusline-")
	if err != nil {
		return nil, err
	}
	r.theme = filepath.Join(r.dir, "theme.json")
	if err := os.WriteFile(r.theme, data, 0600); err != nil {
		os.RemoveAll(r.dir)
		return nil, err
	}
	r.unknownTheme = filepath.Join(r.dir, "theme-unknown.json")
	unknown, err := withoutShellSegments(data)
	if err != nil {
		os.RemoveAll(r.dir)
		return nil, err
	}
	if err = os.WriteFile(r.unknownTheme, unknown, 0600); err != nil {
		os.RemoveAll(r.dir)
		return nil, err
	}
	return r, nil
}

// Keep filesystem context without claiming a successful command or duration.
// Both files are disposable derivatives of the same authoritative user theme.
func withoutShellSegments(data []byte) ([]byte, error) {
	var theme map[string]any
	if err := json.Unmarshal(data, &theme); err != nil {
		return nil, err
	}
	blocks, _ := theme["blocks"].([]any)
	for _, entry := range blocks {
		block, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		segments, ok := block["segments"].([]any)
		if !ok {
			continue
		}
		kept := make([]any, 0, len(segments))
		for _, entry := range segments {
			segment, ok := entry.(map[string]any)
			if ok {
				switch segment["type"] {
				case "status", "executiontime", "jobs":
					continue
				}
			}
			kept = append(kept, entry)
		}
		block["segments"] = kept
	}
	return json.Marshal(theme)
}

func (r *Renderer) Placement() placement.Anchor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cfg.Placement
}

func (r *Renderer) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	return os.RemoveAll(r.dir)
}

func (r *Renderer) Render(ctx context.Context, input Input) (string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed {
		return "", errors.New("renderer is closed")
	}
	ctx, cancel := context.WithTimeout(ctx, r.cfg.Timeout)
	defer cancel()
	input.Cwd = normalizePath(input.Cwd)
	if !filepath.IsAbs(input.Cwd) {
		return "", errors.New("render cwd must be an absolute local path")
	}
	env := baseEnv(input.Env)
	env["BSP_USER_ID"] = "unknown"
	if input.UserID != nil {
		env["BSP_USER_ID"] = strconv.Itoa(*input.UserID)
	}
	theme := r.theme
	env["BSP_SHELL_CONTEXT"] = "known"
	if !input.ShellKnown {
		theme = r.unknownTheme
		input.Jobs = 0
		input.ExitCode = 0
		input.DurationMS = 0
		env["BSP_SHELL_CONTEXT"] = "unknown"
	}
	began := time.Now()
	values := nativeSegmentsFor(ctx, r, input, env, r.dependencies)
	timing.Record(ctx, "native_segments_done", began)
	for key, value := range values {
		env[key] = value
	}
	env["PWD"] = input.Cwd
	key := renderKey(input, env)
	// Polls still resolve every declared native dependency, including Git status.
	// Only OMP execution is skipped, and only for a proven input-only theme.
	if r.cacheable {
		if line, ok := r.cached(ctx, key); ok {
			timing.Record(ctx, "render_cache_hit", time.Time{})
			return line, nil
		}
	}
	args := []string{"print", "primary", "--config", theme, "--pwd", input.Cwd, "--shell", "uni", "--escape=false", "--terminal-width", strconv.Itoa(input.Columns)}
	if input.ShellKnown {
		args = append(args, "--execution-time", strconv.FormatInt(input.DurationMS, 10), "--status", strconv.Itoa(input.ExitCode), "--job-count", strconv.Itoa(input.Jobs))
	}
	out, err := r.run(ctx, r.executable, args, input.Cwd, env, r.cfg.MaxOutputBytes)
	if err != nil {
		return "", fmt.Errorf("render Oh My Posh: %w", err)
	}
	line := Fit(out, input.Columns)
	if r.cacheable {
		r.cacheResult(ctx, key, line)
	}
	return line, nil
}

// The block environment is authoritative: never inherit the manager's virtualenv,
// git overrides, runtime configuration, or shell state.
func baseEnv(input map[string]string) map[string]string {
	env := map[string]string{"PATH": "/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin", "LANG": "C.UTF-8", "TERM": "xterm-256color"}
	if home, err := os.UserHomeDir(); err == nil {
		env["HOME"] = home
	}
	for k, v := range input {
		if k != "" && !strings.ContainsAny(k, "=\x00") && !strings.ContainsRune(v, 0) {
			env[k] = v
		}
	}
	return env
}

type limitedBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.buf.Len() {
		n := b.limit - b.buf.Len()
		if n > 0 {
			b.buf.Write(p[:n])
		}
		return n, errors.New("subprocess output exceeds limit")
	}
	return b.buf.Write(p)
}

func (r *Renderer) run(ctx context.Context, executable string, args []string, cwd string, env map[string]string, limit int) (string, error) {
	out := &limitedBuffer{limit: limit}
	if err := r.runTo(ctx, executable, args, cwd, env, out); err != nil {
		return "", err
	}
	return out.buf.String(), nil
}

// All subprocess consumers share environment isolation, process-group cleanup,
// cancellation, bounded stderr, and timing, including streaming Git status.
func (r *Renderer) runTo(ctx context.Context, executable string, args []string, cwd string, env map[string]string, out io.Writer) error {
	began := time.Now()
	ok := false
	operation := ""
	for _, arg := range args {
		switch arg {
		case "print", "version", "--version", "rev-parse", "symbolic-ref", "remote", "status", "rev-list", "stash", "diff", "config":
			operation = arg
		}
		if operation != "" {
			break
		}
	}
	defer func() {
		timing.Record(ctx, "subprocess_done", began, "tool", filepath.Base(executable), "operation", operation, "ok", ok)
	}()
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Dir = cwd
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	cmd.Env = make([]string, 0, len(keys))
	for _, k := range keys {
		cmd.Env = append(cmd.Env, k+"="+env[k])
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if err == syscall.ESRCH {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = 100 * time.Millisecond
	stderr := &limitedBuffer{limit: 4096}
	cmd.Stdout, cmd.Stderr = out, stderr
	err := cmd.Run()
	// Also terminate descendants which outlive a successful direct child.
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(executable), err)
	}
	ok = true
	return nil
}

func lookup(name string, env map[string]string) string {
	if strings.ContainsRune(name, filepath.Separator) {
		return name
	}
	for _, dir := range filepath.SplitList(env["PATH"]) {
		if !filepath.IsAbs(dir) {
			continue
		}
		path := filepath.Join(dir, name)
		if st, err := os.Stat(path); err == nil && !st.IsDir() && st.Mode()&0111 != 0 {
			return path
		}
	}
	return ""
}

func (r *Renderer) probe(ctx context.Context, cwd string, env map[string]string, name string, args ...string) string {
	executable := lookup(name, env)
	if executable == "" {
		return ""
	}
	out, err := r.run(ctx, executable, args, cwd, env, 64*1024)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

var _ io.Writer = (*limitedBuffer)(nil)
