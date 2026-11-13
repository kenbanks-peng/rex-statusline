package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
)

func TestEnsureLaunchAgentAlignment(t *testing.T) {
	absent := fmt.Errorf("print: %w", exec.Command("/bin/sh", "-c", "exit 113").Run())
	failure := errors.New("launchctl unavailable")
	target := "gui/" + strconv.Itoa(os.Getuid()) + "/" + label
	for _, tc := range []struct {
		name     string
		old      string
		status   string
		printErr error
		want     []string
		wantErr  error
	}{
		{name: "aligned running", old: "desired", status: "state = running\n\tpid = 123\n", want: []string{"print"}},
		{name: "aligned stopped", old: "desired", want: []string{"print", "kickstart"}},
		{name: "aligned unloaded", old: "desired", printErr: absent, want: []string{"print", "bootstrap"}},
		{name: "manual to Homebrew", old: "manual", status: "pid = 123", want: []string{"print", "bootout", "bootstrap"}},
		{name: "Homebrew to manual", old: "Homebrew", want: []string{"print", "bootout", "bootstrap"}},
		{name: "missing registration", printErr: absent, want: []string{"print", "bootstrap"}},
		{name: "changed unloaded", old: "old", printErr: absent, want: []string{"print", "bootstrap"}},
		{name: "inspection failure", old: "old", printErr: failure, want: []string{"print"}, wantErr: failure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "agent.plist")
			if tc.old != "" {
				if err := os.WriteFile(path, []byte(tc.old), 0600); err != nil {
					t.Fatal(err)
				}
			}
			var calls []string
			err := ensureLaunchAgent(context.Background(), path, []byte("desired"), func(_ context.Context, args ...string) ([]byte, error) {
				calls = append(calls, args[0])
				if args[0] == "bootstrap" {
					if !reflect.DeepEqual(args, []string{"bootstrap", "gui/" + strconv.Itoa(os.Getuid()), path}) {
						t.Fatalf("args: %v", args)
					}
				} else if !reflect.DeepEqual(args, []string{args[0], target}) {
					t.Fatalf("args: %v", args)
				}
				if args[0] == "print" {
					return []byte(tc.status), tc.printErr
				}
				return nil, nil
			})
			if !errors.Is(err, tc.wantErr) || !reflect.DeepEqual(calls, tc.want) {
				t.Fatalf("error %v, calls %v", err, calls)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			want := "desired"
			if tc.wantErr != nil {
				want = tc.old
			}
			if string(data) != want {
				t.Fatalf("plist %q, want %q", data, want)
			}
		})
	}
}

func TestEnsureLaunchAgentFailures(t *testing.T) {
	for _, command := range []string{"bootout", "bootstrap", "kickstart"} {
		t.Run(command, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "agent.plist")
			current := "old"
			if command == "kickstart" {
				current = "desired"
			}
			if err := os.WriteFile(path, []byte(current), 0600); err != nil {
				t.Fatal(err)
			}
			failure := errors.New("failed " + command)
			err := ensureLaunchAgent(context.Background(), path, []byte("desired"), func(_ context.Context, args ...string) ([]byte, error) {
				if args[0] == command {
					return nil, failure
				}
				return nil, nil
			})
			if !errors.Is(err, failure) {
				t.Fatalf("got %v, want %v", err, failure)
			}
		})
	}
	t.Run("canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := ensureLaunchAgent(ctx, "/unused", nil, func(context.Context, ...string) ([]byte, error) {
			t.Fatal("launchctl called after cancellation")
			return nil, nil
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v", err)
		}
	})
}

func TestLaunchAgentExecutable(t *testing.T) {
	for _, tc := range []struct {
		name     string
		brew     bool
		stable   bool
		mismatch bool
	}{
		{name: "manual"},
		{name: "Homebrew stable alias", brew: true, stable: true},
		{name: "Homebrew missing alias", brew: true},
		{name: "different Homebrew version", brew: true, stable: true, mismatch: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, ".local", "bin", "rex-statusline")
			if tc.brew {
				path = filepath.Join(root, "Cellar", "rex-statusline", "1.0.1", "bin", "rex-statusline")
			}
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("binary"), 0755); err != nil {
				t.Fatal(err)
			}
			stable := filepath.Join(root, "opt", "rex-statusline")
			if tc.stable {
				if err := os.MkdirAll(filepath.Dir(stable), 0755); err != nil {
					t.Fatal(err)
				}
				destination := filepath.Dir(filepath.Dir(path))
				if tc.mismatch {
					destination = filepath.Join(root, "Cellar", "rex-statusline", "2.0.0")
					if err := os.MkdirAll(filepath.Join(destination, "bin"), 0755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(destination, "bin", "rex-statusline"), []byte("other"), 0755); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.Symlink(destination, stable); err != nil {
					t.Fatal(err)
				}
			}
			got, err := launchAgentExecutable(path)
			want := path
			if tc.stable && !tc.mismatch {
				want = filepath.Join(stable, "bin", "rex-statusline")
			}
			if err != nil || got != want {
				t.Fatalf("got %q, %v; want %q", got, err, want)
			}
		})
	}
}
