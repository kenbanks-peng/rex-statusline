package render

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func hostnameCommand(t *testing.T) (string, map[string]string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "scutil")
	script := "#!/bin/sh\nprintf 'call\\n' >> \"$CALLS\"\n" +
		"if [ -f \"$MODE\" ]; then read mode < \"$MODE\"; else mode=ok; fi\n" +
		"case \"$mode\" in\nfail) exit 1;;\nsleep) : > \"$STARTED\"; /bin/sleep 10;;\nesac\n" +
		"printf '%s' \"$LABEL\"\n"
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return path, baseEnv(map[string]string{
		"CALLS": filepath.Join(dir, "calls"), "MODE": filepath.Join(dir, "mode"),
		"STARTED": filepath.Join(dir, "started"), "LABEL": "kens-mac-mini",
	})
}

func hostnameCalls(t *testing.T, env map[string]string) int {
	t.Helper()
	data, err := os.ReadFile(env["CALLS"])
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(data), "call\n")
}

func TestStaticHostnameConcurrentAndContextual(t *testing.T) {
	executable, env := hostnameCommand(t)
	r := &Renderer{}
	cwd := t.TempDir()
	const workers = 20
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got := r.staticHostname(context.Background(), cwd, env, executable); got != "kens-mac-mini" {
				t.Errorf("host %q", got)
			}
		}()
	}
	wg.Wait()
	if got := hostnameCalls(t, env); got != 1 {
		t.Fatalf("calls %d want 1", got)
	}
	// The same executable under another PATH/env/cwd is a separate context.
	env["PATH"] = "/bin"
	r.staticHostname(context.Background(), cwd, env, executable)
	env["LABEL"] = "kens-macbook-air"
	if got := r.staticHostname(context.Background(), cwd, env, executable); got != "kens-macbook-air" {
		t.Fatal(got)
	}
	r.staticHostname(context.Background(), t.TempDir(), env, executable)
	if got := hostnameCalls(t, env); got != 4 {
		t.Fatalf("calls %d want 4", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := r.staticHostname(ctx, cwd, env, executable); got != "" {
		t.Fatalf("canceled cache hit %q", got)
	}
}

func TestStaticHostnameFailureRetries(t *testing.T) {
	executable, env := hostnameCommand(t)
	r := &Renderer{}
	cwd := t.TempDir()
	for _, mode := range []string{"fail", "ok"} {
		if err := os.WriteFile(env["MODE"], []byte(mode+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		got := r.staticHostname(context.Background(), cwd, env, executable)
		if (mode == "fail" && got != "") || (mode == "ok" && got != env["LABEL"]) {
			t.Fatal(mode, got)
		}
	}
	if got := hostnameCalls(t, env); got != 2 {
		t.Fatalf("calls %d want 2", got)
	}
}

func TestStaticHostnameCanceledLeaderDoesNotPoisonWaiter(t *testing.T) {
	executable, env := hostnameCommand(t)
	if err := os.WriteFile(env["MODE"], []byte("sleep\n"), 0600); err != nil {
		t.Fatal(err)
	}
	r := &Renderer{}
	cwd := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	leader := make(chan string, 1)
	go func() { leader <- r.staticHostname(ctx, cwd, env, executable) }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(env["STARTED"]); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("leader did not start")
		}
		time.Sleep(time.Millisecond)
	}
	// A canceled follower must not wait for the leader's subprocess.
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer waitCancel()
	start := time.Now()
	if got := r.staticHostname(waitCtx, cwd, env, executable); got != "" || time.Since(start) > time.Second {
		t.Fatal("waiter ignored cancellation")
	}
	waiter := make(chan string, 1)
	go func() { waiter <- r.staticHostname(context.Background(), cwd, env, executable) }()
	if err := os.WriteFile(env["MODE"], []byte("ok\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case got := <-leader:
		if got != "" {
			t.Fatalf("canceled leader %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("leader ignored cancellation")
	}
	select {
	case got := <-waiter:
		if got != env["LABEL"] {
			t.Fatalf("waiter %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("waiter did not retry")
	}
	if got := hostnameCalls(t, env); got != 2 {
		t.Fatalf("calls %d want 2", got)
	}
}
