package app

import (
	"context"
	"errors"
	"os"
	"testing"

	"rex-statusline/internal/bsp"
	"rex-statusline/internal/manager"
	"rex-statusline/internal/rex"
)

type processSequence struct {
	calls     int
	processes []rex.Process
}

func (a *processSequence) Call(ctx context.Context, _, _ string, _, out any) error {
	if size, ok := out.(*rex.Size); ok {
		*size = rex.Size{Columns: 80, Rows: 24}
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	i := min(a.calls, len(a.processes)-1)
	a.calls++
	*out.(*rex.Process) = a.processes[i]
	return nil
}

func TestPrepareRetriesWithFreshOwnership(t *testing.T) {
	pid := os.Getpid()
	old, next := t.TempDir(), t.TempDir()
	api := &processSequence{processes: []rex.Process{
		{Foreground: rex.ProcessEntry{PID: pid, Name: "zsh", Cwd: next}},
		{Foreground: rex.ProcessEntry{PID: pid, Name: "ssh", Cwd: next}},
	}}
	source := manager.Source{SessionID: "session:s", Block: rex.Block{ID: "block:b", HostID: "host"}, Size: rex.Size{Columns: 80}}
	resolver := Resolver{API: api, Identity: rex.Identity{InstanceID: "instance", HostID: "host"}, Root: t.TempDir()}
	if err := os.Chmod(resolver.Root, 0700); err != nil {
		t.Fatal(err)
	}
	store := bsp.Store{Root: resolver.Root, ServerID: "instance", SessionID: source.SessionID}
	if err := store.Publish(source.Block.ID, pid, "local", bsp.Report{Kind: "context", Context: bsp.Context{Cwd: old}}); err != nil {
		t.Fatal(err)
	}
	_, prepared, err := resolver.Prepare(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	line, err := prepared(context.Background(), source)
	if err != nil || line != "Remote/container status unavailable" || api.calls != 2 {
		t.Fatalf("line %q err %v process calls %d", line, err, api.calls)
	}
}

func TestPrepareIsRequestScopedAndBlockScoped(t *testing.T) {
	api := &processSequence{processes: []rex.Process{
		{Foreground: rex.ProcessEntry{PID: 123, Name: "ssh"}},
	}}
	resolver := Resolver{API: api, Identity: rex.Identity{HostID: "host"}}
	source := manager.Source{SessionID: "session:s", Block: rex.Block{ID: "block:b", HostID: "host"}}
	for range 2 {
		_, prepared, err := resolver.Prepare(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		other := source
		other.Block.ID = "block:other"
		if _, err := prepared(context.Background(), other); !errors.Is(err, bsp.ErrOwnership) {
			t.Fatal(err)
		}
	}
	if api.calls != 2 {
		t.Fatalf("cached process across requests: %d calls", api.calls)
	}
}

func TestRemoteHostSkipsUnnecessaryProcessRead(t *testing.T) {
	api := &processSequence{}
	resolver := Resolver{API: api, Identity: rex.Identity{HostID: "local"}}
	source := manager.Source{Block: rex.Block{HostID: "remote"}}
	_, prepared, err := resolver.Prepare(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	line, err := prepared(context.Background(), source)
	if err != nil || line != "Remote Rex host status unavailable" {
		t.Fatalf("%q %v", line, err)
	}
	if _, err := resolver.Render(context.Background(), source); err != nil {
		t.Fatal(err)
	}
	if api.calls != 0 {
		t.Fatalf("unnecessary remote calls %d", api.calls)
	}
}
