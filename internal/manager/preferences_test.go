package manager

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestPreferencesOverlapDiscoveryAndJoinBeforeGeometry(t *testing.T) {
	f := fixture()
	m := testManager(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	read := func(ctx context.Context) (*float64, error) {
		started <- struct{}{}
		select {
		case <-release:
			value := 3.0
			return &value, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	m.Padding, m.HorizontalPadding = read, read
	discovered := make(chan struct{}, 1)
	m.API = apiFunc(func(ctx context.Context, s, method string, args, out any) error {
		if method == "session.view" {
			discovered <- struct{}{}
		}
		return f.Call(ctx, s, method, args, out)
	})
	var rendered atomic.Bool
	m.Render = func(context.Context, Source) (string, error) { rendered.Store(true); return "ready", nil }
	done := make(chan error, 1)
	go func() { done <- m.Reconcile(ctx) }()
	for range 2 {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("preference reads were serial")
		}
	}
	select {
	case <-discovered:
	case <-ctx.Done():
		t.Fatal("preferences blocked discovery")
	}
	if rendered.Load() {
		t.Fatal("geometry/render started before preferences were ready")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if m.padding == nil || *m.padding != 3 || m.horizontalPadding == nil || *m.horizontalPadding != 3 {
		t.Fatal("preferences not applied")
	}
}
