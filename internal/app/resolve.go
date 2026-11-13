package app

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"rex-statusline/internal/bsp"
	"rex-statusline/internal/manager"
	"rex-statusline/internal/render"
	"rex-statusline/internal/rex"
	"rex-statusline/internal/timing"
)

type Resolver struct {
	API      rex.API
	Identity rex.Identity
	Root     string
	Renderer *render.Renderer
}

func ProcessContext(process rex.Process, remoteHost bool) bsp.Process {
	p := bsp.Process{PIDs: map[int]bool{}, Remote: remoteHost}
	chain := append([]rex.ProcessEntry{process.Foreground}, process.Ancestors...)
	chain = append(chain, process.Child)
	for _, entry := range chain {
		if entry.PID > 1 {
			p.PIDs[entry.PID] = true
		}
		remote := false
		for _, name := range []string{"ssh", "mosh", "docker", "podman", "linux", "container"} {
			if entry.Name == name || filepath.Base(entry.Script) == name {
				remote = true
				p.Remote = true
			}
		}
		if !remote && entry.Script == "" && p.ShellPID == 0 {
			switch entry.Name {
			case "zsh", "bash", "fish", "sh", "dash":
				p.ShellPID = entry.PID
			}
		}
	}
	return p
}
func (r Resolver) Process(ctx context.Context, s manager.Source) (rex.Process, bsp.Process, error) {
	var process rex.Process
	err := r.API.Call(ctx, s.SessionID, "block.method", map[string]any{"block_id": s.Block.ID, "method": "process", "args": map[string]any{}}, &process)
	return process, ProcessContext(process, s.Block.HostID != r.Identity.HostID), err
}
func (r Resolver) Render(ctx context.Context, s manager.Source) (string, error) {
	return r.render(ctx, s, true)
}

// Prepare reads fresh dimensions and ownership together. BSP retries still
// re-read process ownership; no terminal state is cached across requests.
func (r Resolver) Prepare(ctx context.Context, s manager.Source) (rex.Size, manager.RenderFunc, error) {
	local := s.Block.HostID == r.Identity.HostID
	terminal, err := rex.ReadTerminal(ctx, r.API, s.SessionID, s.Block.ID, local)
	if err != nil {
		return terminal.Size, nil, err
	}
	return terminal.Size, func(ctx context.Context, sized manager.Source) (string, error) {
		if sized.SessionID != s.SessionID || sized.Block.ID != s.Block.ID || sized.Block.HostID != s.Block.HostID {
			return "", bsp.ErrOwnership
		}
		if !local {
			return "Remote Rex host status unavailable", nil
		}
		ownership := ProcessContext(terminal.Process, false)
		return r.renderProcess(ctx, sized, true, terminal.Process, ownership)
	}, nil
}

func (r Resolver) render(ctx context.Context, s manager.Source, retry bool) (string, error) {
	if s.Block.HostID != r.Identity.HostID {
		return "Remote Rex host status unavailable", nil
	}
	began := time.Now()
	process, ownership, err := r.Process(ctx, s)
	timing.Record(ctx, "process_done", began, "ok", err == nil)
	if err != nil {
		return "", err
	}
	return r.renderProcess(ctx, s, retry, process, ownership)
}

func (r Resolver) renderProcess(ctx context.Context, s manager.Source, retry bool, process rex.Process, ownership bsp.Process) (string, error) {
	store := bsp.Store{Root: r.Root, ServerID: r.Identity.InstanceID, SessionID: s.SessionID}
	began := time.Now()
	report, readErr := store.Read(s.Block.ID, ownership)
	timing.Record(ctx, "bsp_read_done", began, "ok", readErr == nil)
	if readErr == nil {
		if report.Kind == "line" {
			return render.Fit(render.Sanitize(report.Line), s.Size.Columns), nil
		}
		if report.Kind == "context" {
			// Rex owns current cwd. A shell report from a previous directory must not
			// override a foreground program's actual location.
			cwd := normalize(process.Foreground.Cwd)
			if cwd == "" || cwd == normalize(report.Context.Cwd) {
				c := report.Context
				return r.Renderer.Render(ctx, render.Input{ShellKnown: true, UserID: process.Foreground.UserID, Cwd: c.Cwd, Env: c.Env, Jobs: c.Jobs, ExitCode: c.ExitCode, DurationMS: c.DurationMS, Columns: s.Size.Columns})
			}
			// Cwd notifications can precede precmd's environment publication.
			// Re-read rather than mixing the new directory with the old environment.
			if retry && !ownership.Remote {
				began = time.Now()
				timer := time.NewTimer(150 * time.Millisecond)
				defer timer.Stop()
				select {
				case <-ctx.Done():
					return "", ctx.Err()
				case <-timer.C:
					timing.Record(ctx, "context_retry_done", began)
					return r.render(ctx, s, false)
				}
			}
		}
	}
	if ownership.Remote {
		return "Remote/container status unavailable", nil
	}
	cwd := normalize(process.Foreground.Cwd)
	if cwd == "" {
		return "Block directory unavailable", nil
	}
	// Only filesystem-derived context is available without a publisher. Never
	// substitute manager shell environment, job count, exit status or duration.
	line, err := r.Renderer.Render(ctx, render.Input{UserID: process.Foreground.UserID, Cwd: cwd, Columns: s.Size.Columns})
	if err != nil {
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			return "", fmt.Errorf("BSP: %v; render: %w", readErr, err)
		}
		return "", err
	}
	return line, nil
}
func normalize(s string) string {
	if strings.HasPrefix(s, "file:") {
		u, err := url.Parse(s)
		if err != nil || u.Scheme != "file" || (u.Host != "" && u.Host != "localhost") || u.RawQuery != "" || u.Fragment != "" {
			return ""
		}
		s = u.Path
	}
	if !filepath.IsAbs(s) {
		return ""
	}
	return filepath.Clean(s)
}
