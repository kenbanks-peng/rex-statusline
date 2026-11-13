package render

import (
	"context"
	"crypto/sha256"
	"encoding/json"

	"rex-statusline/internal/timing"
)

type cachedRender struct {
	key  [32]byte
	line string
}

func renderKey(input Input, env map[string]string) [32]byte {
	data, _ := json.Marshal(struct {
		Input Input
		Env   map[string]string
	}{input, env})
	return sha256.Sum256(data)
}

func (r *Renderer) cached(ctx context.Context, key [32]byte) (string, bool) {
	trace := timing.From(ctx)
	if ctx.Err() != nil || trace.Window == "" {
		return "", false
	}
	r.cacheMu.Lock()
	defer r.cacheMu.Unlock()
	cached, ok := r.cache[trace.Window]
	return cached.line, ok && cached.key == key
}

func (r *Renderer) cacheResult(ctx context.Context, key [32]byte, line string) {
	trace := timing.From(ctx)
	if ctx.Err() != nil || trace.Window == "" {
		return
	}
	r.cacheMu.Lock()
	defer r.cacheMu.Unlock()
	if r.cache == nil {
		r.cache = map[string]cachedRender{}
	}
	// Bound closed-window retention independently of freshness. Freshness comes
	// from the audited theme and freshly resolved dependencies, never a TTL.
	if len(r.cache) >= 64 {
		for window := range r.cache {
			delete(r.cache, window)
			break
		}
	}
	r.cache[trace.Window] = cachedRender{key: key, line: line}
}
