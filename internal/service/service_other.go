//go:build !darwin && !linux

package service

import (
	"context"
	"errors"
)

func Stop(context.Context) error {
	return errors.New("background services require macOS or Linux with systemd; stop the foreground manager with Ctrl-C or through your service manager")
}

func Ensure(context.Context, []string) error {
	return errors.New("background services require macOS or Linux with systemd; use manage --foreground under your service manager")
}
