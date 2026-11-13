package bridge

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"rex-statusline/internal/bsp"
)

func TestParse(t *testing.T) {
	now := time.Now()
	data, _ := json.Marshal(Message{Version: 1, Kind: "line", Line: "界", Updated: now})
	if m, err := Parse(data, now); err != nil || m.Line != "界" {
		t.Fatalf("%+v %v", m, err)
	}
	for _, raw := range []string{
		`{}`, string(data) + "{}", `{"version":1,"kind":"context"}`,
		`{"version":1,"kind":"line","line":"x","owner":1}`,
	} {
		if _, err := Parse([]byte(raw), now); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	if _, err := Parse(data, now.Add(3*time.Minute)); err == nil {
		t.Fatal("accepted stale message")
	}
	if _, err := Parse(data, now.Add(-2*time.Minute)); err == nil {
		t.Fatal("accepted future message")
	}
}
func privateDir(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	if err := os.Chmod(d, 0700); err != nil {
		t.Fatal(err)
	}
	return d
}
func TestForwardAndRelease(t *testing.T) {
	inbox := privateDir(t)
	store := bsp.Store{Root: privateDir(t), ServerID: "instance", SessionID: "session:s"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	b := Bridge{Store: store, Block: "block:b", Owner: os.Getpid(), Source: "container", Inbox: inbox}
	go func() { done <- b.Run(ctx) }()
	check := func(want string) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			r, err := store.Read("block:b", bsp.Process{PIDs: map[int]bool{os.Getpid(): true}})
			if err == nil && r.Line == want {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("missing report", want)
	}
	check("")
	data, _ := json.Marshal(Message{Version: 1, Kind: "line", Line: "guest", Updated: time.Now()})
	if err := os.WriteFile(filepath.Join(inbox, "report.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	check("guest")
	r, _ := store.Read("block:b", bsp.Process{PIDs: map[int]bool{os.Getpid(): true}})
	path := filepath.Join(store.Root, store.ServerID, store.SessionID, "block:b", "report.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(250 * time.Millisecond)
	r2, _ := store.Read("block:b", bsp.Process{PIDs: map[int]bool{os.Getpid(): true}})
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if r.Epoch != r2.Epoch || string(before) != string(after) {
		t.Fatal("guest silence was converted to a heartbeat")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read("block:b", bsp.Process{PIDs: map[int]bool{os.Getpid(): true}}); !os.IsNotExist(err) {
		t.Fatalf("release: %v", err)
	}
}
func TestUnsafeInbox(t *testing.T) {
	target := privateDir(t)
	link := filepath.Join(t.TempDir(), "link")
	os.Symlink(target, link)
	b := Bridge{Store: bsp.Store{Root: privateDir(t), ServerID: "s", SessionID: "s"}, Block: "b", Owner: os.Getpid(), Source: "remote", Inbox: link}
	if err := b.Run(context.Background()); err == nil {
		t.Fatal("accepted symlink inbox")
	}
	dir := privateDir(t)
	os.Symlink("/etc/passwd", filepath.Join(dir, "report.json"))
	fd, err := os.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer fd.Close()
	if _, err = read(int(fd.Fd())); err == nil {
		t.Fatal("accepted report symlink")
	}
}
