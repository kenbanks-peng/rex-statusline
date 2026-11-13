package render

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"strings"
)

type hostnameLookup struct {
	done chan struct{}
	host string
}

// Only successful static lookups are retained. The complete subprocess context
// isolates overrides; in particular no environment or cwd crosses windows.
func (r *Renderer) staticHostname(ctx context.Context, cwd string, env map[string]string, executable string) string {
	data, _ := json.Marshal(struct {
		Cwd, Executable string
		Env             map[string]string
	}{cwd, executable, env})
	key := sha256.Sum256(data)
	for {
		if ctx.Err() != nil {
			return ""
		}
		r.hostMu.Lock()
		if entry := r.hosts[key]; entry != nil {
			r.hostMu.Unlock()
			select {
			case <-ctx.Done():
				return ""
			case <-entry.done:
				// A canceled or failed leader must not poison another caller.
				if entry.host != "" {
					return entry.host
				}
				continue
			}
		}
		entry := &hostnameLookup{done: make(chan struct{})}
		if r.hosts == nil {
			r.hosts = make(map[[32]byte]*hostnameLookup)
		}
		// Bound retained contexts without disturbing in-flight waiters.
		if len(r.hosts) >= 64 {
			for oldKey, old := range r.hosts {
				select {
				case <-old.done:
					delete(r.hosts, oldKey)
				default:
				}
			}
		}
		r.hosts[key] = entry
		r.hostMu.Unlock()

		out, err := r.run(ctx, executable, []string{"--get", "LocalHostName"}, cwd, env, 64*1024)
		r.hostMu.Lock()
		if err == nil && ctx.Err() == nil {
			entry.host = strings.TrimSpace(out)
		}
		if entry.host == "" {
			delete(r.hosts, key)
		}
		close(entry.done)
		r.hostMu.Unlock()
		return entry.host
	}
}
