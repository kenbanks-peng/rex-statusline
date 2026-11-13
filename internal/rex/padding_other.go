//go:build !darwin

package rex

import "context"

func TerminalPadding(context.Context) (*float64, error) {
	return nil, nil
}

func TerminalPaddingX(ctx context.Context) (*float64, error) { return nil, nil }
