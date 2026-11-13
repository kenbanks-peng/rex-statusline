package service

import "context"

func Ensure(ctx context.Context, args []string) error {
	return ensureSystemd(ctx, args, systemctl)
}

func Stop(ctx context.Context) error {
	return systemctl(ctx, "stop", systemdUnit)
}
