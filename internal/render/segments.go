package render

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"rex-statusline/internal/timing"

	"github.com/charmbracelet/x/ansi"
)

func normalizePath(path string) string {
	if strings.HasPrefix(path, "file:") {
		u, err := url.Parse(path)
		if err != nil || u.Scheme != "file" || (u.Host != "" && u.Host != "localhost") {
			return path
		}
		path = u.Path
	}
	if path == "" {
		return ""
	}
	return filepath.Clean(path)
}

func directoryPath(path, home string) string {
	path = normalizePath(path)
	home = normalizePath(home)
	icon := " "
	if home != "" {
		for _, s := range []struct{ path, icon, label string }{
			{filepath.Join(home, ".local"), "", "LOCAL"}, {filepath.Join(home, ".cache"), "", "CACHE"},
			{filepath.Join(home, ".config"), "󰒓", "CONFIG"}, {home, "", "HOME"},
		} {
			if path == s.path {
				return s.icon + " " + s.label
			}
			if strings.HasPrefix(path, s.path+string(filepath.Separator)) {
				path = strings.TrimPrefix(path, s.path)
				icon = s.icon
				break
			}
		}
	}
	parts := strings.Split(path, "/")
	last := parts[len(parts)-1]
	if ansi.StringWidth(last) > 20 {
		return ansi.Truncate(last, 20, "...")
	}
	short := last
	for i := len(parts) - 2; i >= 0; i-- {
		candidate := parts[i] + "/" + short
		if ansi.StringWidth(candidate) > 20 {
			short = ".." + short
			break
		}
		short = candidate
	}
	return icon + short
}

func exists(dir, name string) bool {
	st, err := os.Stat(filepath.Join(dir, name))
	return err == nil && !st.IsDir()
}

func ancestors(dir string) []string {
	var dirs []string
	for {
		dirs = append(dirs, dir)
		next := filepath.Dir(dir)
		if next == dir {
			return dirs
		}
		dir = next
	}
}

func fileExtensions(dir string) map[string]bool {
	found := map[string]bool{}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if !e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			found[filepath.Ext(e.Name())] = true
		}
	}
	return found
}

func packageVersion(path string) string {
	data, err := os.ReadFile(path)
	if err != nil || len(data) > 1024*1024 {
		return ""
	}
	var pkg struct{ Version string }
	if json.Unmarshal(data, &pkg) != nil {
		return ""
	}
	return pkg.Version
}

func versionField(out string) string {
	lines := strings.Split(out, "\n")
	fields := strings.Fields(lines[0])
	if len(fields) > 1 {
		return fields[1]
	}
	return ""
}

func nativeSegmentsFor(ctx context.Context, r *Renderer, in Input, env map[string]string, deps nativeDependencies) map[string]string {
	values := map[string]string{"DIRPATH": directoryPath(in.Cwd, env["HOME"]), "JOBCOUNT": "", "JS_SEGMENT": "", "PYTHON_SEGMENT": "",
		"GITSTATUS": "", "GITSTATUS_CHANGED": "0", "GITSTATUS_AHEAD": "0", "GITSTATUS_BEHIND": "0", "POSH_HOST_LABEL": ""}
	if in.Jobs > 0 {
		values["JOBCOUNT"] = fmt.Sprintf(" %d", in.Jobs)
	}
	began := time.Now()
	if deps&nativeHost != 0 {
		host, _ := os.Hostname()
		if runtime.GOOS == "darwin" {
			if local := r.staticHostname(ctx, in.Cwd, env, "/usr/sbin/scutil"); local != "" {
				host = local
			}
		}
		values["POSH_HOST_LABEL"] = env["POSH_HOST_LABEL"]
		switch strings.ToLower(strings.TrimSuffix(host, ".local")) {
		case "kens-macbook-air":
			values["POSH_HOST_LABEL"] = "laptop"
		case "kens-mac-mini":
			values["POSH_HOST_LABEL"] = "macmini"
		}
		timing.Record(ctx, "segment_done", began, "segment", "host")
	}
	began = time.Now()
	var exts map[string]bool
	if deps&(nativePython|nativeJavaScript) != 0 {
		exts = fileExtensions(in.Cwd)
	}
	if deps&nativePython != 0 {
		py := ""
		if exts[".py"] {
			py = "󱔎"
		}
		for _, dir := range ancestors(in.Cwd) {
			if exists(dir, "pyproject.toml") {
				py = "󱔎"
				python := "python3"
				if env["VIRTUAL_ENV"] != "" {
					py = ""
					python = filepath.Join(env["VIRTUAL_ENV"], "bin", "python")
				}
				v := versionField(r.probe(ctx, in.Cwd, env, python, "--version"))
				if v == "" {
					v = "NONE"
				}
				py += " " + v
				break
			}
		}
		values["PYTHON_SEGMENT"] = py
		timing.Record(ctx, "segment_done", began, "segment", "python")
	}
	began = time.Now()
	if deps&nativeJavaScript != 0 {
		icon, bun, runtime, version := "", "", "", ""
		if exts[".ts"] {
			icon = ""
		} else if exts[".js"] {
			icon = ""
		}
		for _, dir := range ancestors(in.Cwd) {
			if exists(dir, "bun.lock") || exists(dir, "bunfig.toml") {
				bun = ""
			}
			if exists(dir, "deno.json") || exists(dir, "deno.jsonc") {
				icon = ""
				runtime = ""
				version = versionField(r.probe(ctx, in.Cwd, env, "deno", "--version"))
				break
			}
			if exists(dir, "svelte.config.js") {
				icon = ""
				runtime = ""
				version = packageVersion(filepath.Join(dir, "node_modules/svelte/package.json"))
				break
			}
			if exists(dir, "package.json") {
				data, _ := os.ReadFile(filepath.Join(dir, "package.json"))
				var pkg struct {
					Dependencies     map[string]any
					DevDependencies  map[string]any
					PeerDependencies map[string]any
				}
				_ = json.Unmarshal(data, &pkg)
				_, dep := pkg.Dependencies["react-dom"]
				_, dev := pkg.DevDependencies["react-dom"]
				_, peer := pkg.PeerDependencies["react-dom"]
				if dep || dev || peer {
					runtime = ""
					version = packageVersion(filepath.Join(dir, "node_modules/react-dom/package.json"))
				} else {
					runtime = "󰆧"
					version = strings.TrimPrefix(r.probe(ctx, in.Cwd, env, "node", "--version"), "v")
				}
				break
			}
		}
		var components []string
		for _, v := range []string{icon, bun, runtime, version} {
			if v != "" {
				components = append(components, v)
			}
		}
		js := strings.Join(components, " ")
		if runtime == "" {
			js = pad(js)
		}
		values["JS_SEGMENT"] = js
		timing.Record(ctx, "segment_done", began, "segment", "javascript")
	}
	began = time.Now()
	if deps&nativeGit != 0 {
		gitSegments(ctx, r, in.Cwd, env, values)
		timing.Record(ctx, "segment_done", began, "segment", "git")
	}
	// Dynamic text is not allowed to inject OMP inline color/template markup.
	for k, v := range values {
		values[k] = strings.NewReplacer("<", "‹", ">", "›").Replace(stripSGR(Sanitize(v)))
	}
	return values
}

func stripSGR(s string) string { return ansi.Strip(s) }
func pad(s string) string {
	if s == "" {
		return s
	}
	n := 10 - ansi.StringWidth(s)
	if n <= 0 {
		return s
	}
	return strings.Repeat(" ", n/2) + s + strings.Repeat(" ", n-n/2)
}

func gitSegments(ctx context.Context, r *Renderer, cwd string, env, values map[string]string) {
	// Avoid inherited Git process hooks/config from the manager; target overrides
	// remain part of its explicitly supplied context.
	git := func(args ...string) string {
		return r.probe(ctx, cwd, env, "git", append([]string{"--no-optional-locks", "-c", "core.fsmonitor=false"}, args...)...)
	}
	state, err := r.gitStatus(ctx, cwd, env)
	if err != nil {
		return
	}
	branch := state.branch
	if branch == "(detached)" {
		// Let Git choose its configured, unambiguous abbreviation.
		branch = git("rev-parse", "--short", "HEAD")
	}
	fullBranch := branch
	if ansi.StringWidth(branch) > 25 {
		branch = ansi.Truncate(branch, 25, "") + "…"
	}
	// Fetch both remotes and URL rewrites together, afresh on every render.
	remotes := gitRemoteURLs(git("config", "--null", "--get-regexp", "^(remote\\.(origin|upstream)\\.url|url\\..*\\.insteadof)$"))
	origin, upstream := remotes["origin"], remotes["upstream"]
	service := ""
	if origin != "" {
		switch {
		case strings.Contains(origin, "github"):
			service = ""
		case strings.Contains(origin, "gitlab"):
			service = ""
		case strings.Contains(origin, "bitbucket"):
			service = ""
		case strings.Contains(origin, "azure") || strings.Contains(origin, "visualstudio"):
			service = "󰊢"
		default:
			service = ""
		}
	}
	result := strings.TrimSpace(service + " " + branch)
	if state.dirty {
		values["GITSTATUS_CHANGED"] = "1"
		result += " 󱩽"
	}
	counts := func(ref string) (ahead, behind int) {
		if ref == "" {
			return
		}
		fields := strings.Fields(git("rev-list", "--count", "--left-right", ref+"...HEAD", "--"))
		if len(fields) == 2 {
			behind, _ = strconv.Atoi(fields[0])
			ahead, _ = strconv.Atoi(fields[1])
		}
		return
	}
	// Prefer the actual configured tracking branch, not the display-truncated name.
	tracking := state.upstream
	ahead, behind := state.ahead, state.behind
	if !state.hasCounts {
		tracking = ""
		if origin != "" {
			tracking = "refs/remotes/origin/" + fullBranch
			ahead, behind = counts(tracking)
		}
	}
	appendCounts := func(a, b int) {
		if a > 0 {
			result += fmt.Sprintf(" %d", a)
		}
		if b > 0 {
			result += fmt.Sprintf(" %d", b)
		}
	}
	appendCounts(ahead, behind)
	ua, ub := 0, 0
	if upstream != "" && tracking != "upstream/"+fullBranch && tracking != "refs/remotes/upstream/"+fullBranch {
		ua, ub = counts("refs/remotes/upstream/" + fullBranch)
		if ua+ub > 0 {
			result += " "
			appendCounts(ua, ub)
		}
	}
	values["GITSTATUS"] = pad(result)
	values["GITSTATUS_AHEAD"] = strconv.Itoa(ahead + ua)
	values["GITSTATUS_BEHIND"] = strconv.Itoa(behind + ub)
}
