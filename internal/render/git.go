package render

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"strconv"
	"strings"
)

type gitStatus struct {
	branch, upstream string
	ahead, behind    int
	hasCounts, dirty bool
}

const maxGitStatusHeader = 64 * 1024

// Retain branch headers and two-byte record tags, never pathnames. Every byte is
// drained, so worktree size does not turn a valid status into an output-limit
// failure. The only variable-length retained data (headers) remains bounded.
type gitStatusWriter struct {
	state      gitStatus
	record     []byte
	skipRecord bool
}

func (w *gitStatusWriter) Write(p []byte) (int, error) {
	total := len(p)
	for len(p) > 0 {
		end := bytes.IndexByte(p, 0)
		part := p
		if end >= 0 {
			part = p[:end]
		}
		if !w.skipRecord && len(part) > 0 {
			limit := 2
			if (len(w.record) > 0 && w.record[0] == '#') || (len(w.record) == 0 && part[0] == '#') {
				limit = maxGitStatusHeader
				if len(part) > limit-len(w.record) {
					return total - len(p), errors.New("Git status header exceeds limit")
				}
			}
			keep := min(len(part), limit-len(w.record))
			w.record = append(w.record, part[:keep]...)
		}
		if end < 0 {
			break
		}
		if w.skipRecord {
			w.skipRecord = false
		} else {
			w.consumeRecord()
		}
		w.record = w.record[:0]
		p = p[end+1:]
	}
	return total, nil
}

func (w *gitStatusWriter) consumeRecord() {
	record := string(w.record)
	switch {
	case strings.HasPrefix(record, "# branch.head "):
		w.state.branch = strings.TrimPrefix(record, "# branch.head ")
	case strings.HasPrefix(record, "# branch.upstream "):
		w.state.upstream = strings.TrimPrefix(record, "# branch.upstream ")
	case strings.HasPrefix(record, "# branch.ab "):
		fields := strings.Fields(strings.TrimPrefix(record, "# branch.ab "))
		if len(fields) == 2 {
			ahead, aerr := strconv.Atoi(strings.TrimPrefix(fields[0], "+"))
			behind, berr := strconv.Atoi(strings.TrimPrefix(fields[1], "-"))
			if aerr == nil && berr == nil {
				w.state.ahead, w.state.behind, w.state.hasCounts = ahead, behind, true
			}
		}
	case record == "1 ", record == "u ", record == "? ":
		w.state.dirty = true
	case record == "2 ":
		w.state.dirty = true
		w.skipRecord = true // Original rename pathname is the next NUL record.
	}
}

func (r *Renderer) gitStatus(ctx context.Context, cwd string, env map[string]string) (gitStatus, error) {
	executable := lookup("git", env)
	if executable == "" {
		return gitStatus{}, exec.ErrNotFound
	}
	var out gitStatusWriter
	args := []string{"--no-optional-locks", "-c", "core.fsmonitor=false", "status", "--porcelain=v2", "--branch", "--ahead-behind", "-z"}
	if err := r.runTo(ctx, executable, args, cwd, env, &out); err != nil {
		return gitStatus{}, err
	}
	if len(out.record) != 0 || out.skipRecord {
		return gitStatus{}, io.ErrUnexpectedEOF
	}
	if out.state.branch == "" {
		return gitStatus{}, errors.New("Git status omitted branch header")
	}
	return out.state, nil
}

// config --null separates key and value with a newline and records with NUL.
// Apply the same longest-prefix insteadOf rewrite as remote get-url (first URL).
func gitRemoteURLs(out string) map[string]string {
	remotes := map[string]string{}
	type rewrite struct{ prefix, replacement string }
	var rewrites []rewrite
	for _, record := range strings.Split(out, "\x00") {
		key, value, ok := strings.Cut(record, "\n")
		if !ok {
			continue
		}
		switch key {
		case "remote.origin.url", "remote.upstream.url":
			name := strings.TrimSuffix(strings.TrimPrefix(key, "remote."), ".url")
			if _, seen := remotes[name]; !seen {
				remotes[name] = value
			}
		default:
			if strings.HasPrefix(key, "url.") && strings.HasSuffix(key, ".insteadof") {
				rewrites = append(rewrites, rewrite{value, strings.TrimSuffix(strings.TrimPrefix(key, "url."), ".insteadof")})
			}
		}
	}
	for name, url := range remotes {
		longest := -1
		replacement := url
		for _, rule := range rewrites {
			if strings.HasPrefix(url, rule.prefix) && len(rule.prefix) > longest {
				longest = len(rule.prefix)
				replacement = rule.replacement + strings.TrimPrefix(url, rule.prefix)
			}
		}
		remotes[name] = replacement
	}
	return remotes
}
