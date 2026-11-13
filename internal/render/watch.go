package render

import (
	"context"
	"os"
	"reflect"
	"time"
)

// Watch polls both configuration files, including across atomic saves and symlink changes.
// Callers must wait for Watch to return before closing the renderer.
func (r *Renderer) Watch(ctx context.Context, path string, log func(error)) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	lastError := ""
	for {
		err := r.reload(ctx, path)
		if ctx.Err() != nil {
			return
		}
		message := ""
		if err != nil {
			message = err.Error()
			if message != lastError && log != nil {
				log(err)
			}
		}
		lastError = message
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (r *Renderer) reload(ctx context.Context, path string) error {
	cfg, err := Load(path)
	if err != nil {
		return err
	}
	cfg = withDefaults(cfg)
	r.mu.RLock()
	same := reflect.DeepEqual(cfg, r.cfg)
	r.mu.RUnlock()
	if same {
		return nil
	}
	next, err := New(ctx, cfg)
	if err != nil {
		return err
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return next.Close()
	}
	oldDir := r.dir
	r.cfg, r.executable, r.dir = next.cfg, next.executable, next.dir
	r.theme, r.unknownTheme = next.theme, next.unknownTheme
	r.dependencies, r.cacheable = next.dependencies, next.cacheable
	r.cacheMu.Lock()
	r.cache = nil
	r.cacheMu.Unlock()
	r.mu.Unlock()
	return os.RemoveAll(oldDir)
}
