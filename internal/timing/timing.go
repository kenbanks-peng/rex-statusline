// Package timing records optional, correlated update latency diagnostics.
package timing

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

type Trace struct {
	ID      string    `json:"id"`
	Started time.Time `json:"started"`
	Trigger string    `json:"trigger"`
	Window  string    `json:"window,omitempty"`
	Session string    `json:"session,omitempty"`
	Block   string    `json:"block,omitempty"`
	Phase   string    `json:"phase,omitempty"`
}

type contextKey struct{}
type loggerKey struct{}

func WithLogger(ctx context.Context, logger *Logger) context.Context {
	return context.WithValue(ctx, loggerKey{}, logger)
}

func Record(ctx context.Context, stage string, began time.Time, fields ...any) {
	logger, _ := ctx.Value(loggerKey{}).(*Logger)
	trace := From(ctx)
	logger.Record(trace, trace.Window, stage, began, fields...)
}

func New(trigger string, started time.Time) Trace {
	return Trace{ID: fmt.Sprintf("%d-%d", os.Getpid(), started.UnixNano()), Started: started, Trigger: trigger}
}
func WithTrace(ctx context.Context, trace Trace) context.Context {
	return context.WithValue(ctx, contextKey{}, trace)
}
func From(ctx context.Context) Trace {
	trace, _ := ctx.Value(contextKey{}).(Trace)
	return trace
}

type Logger struct {
	Path string
	file *os.File
	log  *slog.Logger
}

func Open(path string) (*Logger, error) {
	if path == "" {
		return nil, nil
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(absolute, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, err
	}
	return &Logger{Path: absolute, file: file, log: slog.New(slog.NewJSONHandler(file, nil))}, nil
}
func (l *Logger) Close() error {
	if l == nil {
		return nil
	}
	return l.file.Close()
}
func (l *Logger) Record(trace Trace, window, stage string, began time.Time, fields ...any) {
	if l == nil {
		return
	}
	now := time.Now()
	args := []any{"update", trace.ID, "window", window, "trigger", trace.Trigger, "elapsed_ms", float64(now.Sub(trace.Started)) / float64(time.Millisecond)}
	if trace.Session != "" {
		args = append(args, "session", trace.Session)
	}
	if trace.Block != "" {
		args = append(args, "block", trace.Block)
	}
	if trace.Phase != "" {
		args = append(args, "phase", trace.Phase)
	}
	if !began.IsZero() {
		args = append(args, "duration_ms", float64(now.Sub(began))/float64(time.Millisecond))
	}
	args = append(args, fields...)
	l.log.Info(stage, args...)
}
