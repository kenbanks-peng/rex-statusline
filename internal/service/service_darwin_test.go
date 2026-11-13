package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"strconv"
	"testing"
	"time"
)

func TestBootstrap(t *testing.T) {
	exit := func(code string) error {
		return fmt.Errorf("launchctl bootstrap: %w", exec.Command("/bin/sh", "-c", "exit "+code).Run())
	}
	busy, failure := exit("5"), exit("1")
	for _, tc := range []struct {
		name string
		errs []error
	}{
		{name: "first attempt", errs: []error{nil}},
		{name: "teardown race", errs: []error{busy, busy, nil}},
		{name: "other failure", errs: []error{failure}},
		{name: "failure after retry", errs: []error{busy, failure}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			err := bootstrap(context.Background(), "gui/501", "/agent.plist", func(_ context.Context, args ...string) ([]byte, error) {
				if !reflect.DeepEqual(args, []string{"bootstrap", "gui/501", "/agent.plist"}) {
					t.Fatalf("unexpected call: %v", args)
				}
				if calls >= len(tc.errs) {
					t.Fatal("unexpected retry")
				}
				err := tc.errs[calls]
				calls++
				return nil, err
			})
			if !errors.Is(err, tc.errs[len(tc.errs)-1]) || calls != len(tc.errs) {
				t.Fatalf("got %v after %d calls", err, calls)
			}
		})
	}
	t.Run("bounded retries", func(t *testing.T) {
		calls := 0
		err := bootstrap(context.Background(), "gui/501", "/agent.plist", func(context.Context, ...string) ([]byte, error) {
			calls++
			return nil, busy
		})
		if !errors.Is(err, busy) || calls != 21 {
			t.Fatalf("got %v after %d calls", err, calls)
		}
	})
	t.Run("canceled during retry", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		calls := 0
		err := bootstrap(ctx, "gui/501", "/agent.plist", func(context.Context, ...string) ([]byte, error) {
			calls++
			time.AfterFunc(10*time.Millisecond, cancel)
			return nil, busy
		})
		if !errors.Is(err, context.Canceled) || calls != 1 {
			t.Fatalf("got %v after %d calls", err, calls)
		}
	})
	t.Run("already canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := bootstrap(ctx, "gui/501", "/agent.plist", func(context.Context, ...string) ([]byte, error) {
			t.Fatal("called launchctl with canceled context")
			return nil, nil
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v", err)
		}
	})
}

func TestStop(t *testing.T) {
	exit := func(code string) error {
		return exec.Command("/bin/sh", "-c", "exit "+code).Run()
	}
	absent := fmt.Errorf("launchctl bootout: %w", exit("3"))
	failure := fmt.Errorf("launchctl bootout: %w", exit("1"))
	for _, tc := range []struct {
		name   string
		runErr error
		cancel bool
		want   error
	}{
		{name: "loaded"},
		{name: "already stopped", runErr: absent},
		{name: "failure", runErr: failure, want: failure},
		{name: "canceled", runErr: absent, cancel: true, want: context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			err := stop(ctx, func(gotCtx context.Context, args ...string) ([]byte, error) {
				want := []string{"bootout", "gui/" + strconv.Itoa(os.Getuid()) + "/" + label}
				if gotCtx != ctx || !reflect.DeepEqual(args, want) {
					t.Fatalf("unexpected launchctl call: %v", args)
				}
				if tc.cancel {
					cancel()
				}
				return nil, tc.runErr
			})
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}
