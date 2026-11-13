package rex

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTerminalUsesOneCLIAndPassesIdentifiersAsData(t *testing.T) {
	dir := t.TempDir()
	executable, calls := filepath.Join(dir, "rex"), filepath.Join(dir, "calls")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" >> " + calls + "\nprintf '%s\\n' '{\"size\":{\"columns\":80,\"rows\":24},\"process\":{\"foreground\":{\"pid\":123}}}'\n"
	if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	session, block := `session:"; error("injection") --`, `block:"; error("injection") --`
	result, err := ReadTerminal(context.Background(), Client{Executable: executable, Server: "test"}, session, block, true)
	if err != nil || result.Size.Columns != 80 || result.Process.Foreground.PID != 123 {
		t.Fatalf("%+v %v", result, err)
	}
	data, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"session_id": session, "block_id": block, "process": true})
	want := "--autostart=false\n-S\ntest\ndo\n-e\n" + terminalScript + "\n--args\n" + string(args) + "\n--json\n"
	if string(data) != want {
		t.Fatalf("unexpected CLI arguments: %s", data)
	}
}

func TestTerminalRejectsMissingFields(t *testing.T) {
	for _, response := range []string{"{}", `{"size":{"columns":80,"rows":24}}`, "not json"} {
		t.Run(response, func(t *testing.T) {
			executable := filepath.Join(t.TempDir(), "rex")
			if err := os.WriteFile(executable, []byte("#!/bin/sh\nprintf '%s' '"+response+"'\n"), 0700); err != nil {
				t.Fatal(err)
			}
			_, err := (Client{Executable: executable}).Terminal(context.Background(), "s", "b", true)
			if err == nil {
				t.Fatal("accepted incomplete terminal context")
			}
		})
	}
}

func TestTerminalSizeOnlyAndCancellation(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "rex")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nprintf '%s' '{\"size\":{\"columns\":80,\"rows\":24}}'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	client := Client{Executable: executable}
	result, err := client.Terminal(context.Background(), "s", "b", false)
	if err != nil || result.Size.Columns != 80 {
		t.Fatalf("%+v %v", result, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.Terminal(ctx, "s", "b", false); err == nil {
		t.Fatal("ignored cancellation")
	}
}

type terminalAPI func(context.Context, string, string, any, any) error

func (f terminalAPI) Call(ctx context.Context, s, method string, args, out any) error {
	return f(ctx, s, method, args, out)
}

func TestReadTerminalFallbackOverlapsReads(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	size, process := make(chan struct{}), make(chan struct{})
	api := terminalAPI(func(ctx context.Context, _, _ string, _ any, out any) error {
		switch value := out.(type) {
		case *Size:
			close(size)
			select {
			case <-process:
			case <-ctx.Done():
				return ctx.Err()
			}
			*value = Size{Columns: 80, Rows: 24}
		case *Process:
			close(process)
			select {
			case <-size:
			case <-ctx.Done():
				return ctx.Err()
			}
			value.Foreground.PID = 123
		}
		return nil
	})
	result, err := ReadTerminal(ctx, api, "s", "b", true)
	if err != nil || result.Size.Columns != 80 || result.Process.Foreground.PID != 123 {
		t.Fatalf("%+v %v", result, err)
	}
}

func TestReadTerminalFallbackCancelsAndJoinsProcessOnSizeFailure(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		t.Run(map[bool]string{false: "failure", true: "zero size"}[invalid], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			exited := make(chan struct{})
			api := terminalAPI(func(ctx context.Context, _, _ string, _ any, out any) error {
				if _, ok := out.(*Size); ok {
					if invalid {
						return nil
					}
					return errors.New("size unavailable")
				}
				defer close(exited)
				<-ctx.Done()
				return ctx.Err()
			})
			result, err := ReadTerminal(ctx, api, "s", "b", true)
			if result.Size.Columns != 0 || err == nil {
				t.Fatalf("%+v %v", result, err)
			}
			select {
			case <-exited:
			default:
				t.Fatal("process read outlived request")
			}
		})
	}
}
