package display

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"golang.org/x/term"
	"rex-statusline/internal/presentation"
	"rex-statusline/internal/render"
	"rex-statusline/internal/rex"
	"rex-statusline/internal/timing"
)

func Run(ctx context.Context, path string, api rex.API, logger *timing.Logger) error {
	pointer, err := screenPointer()
	if err != nil {
		return err
	}
	drag := drag{api: api, session: os.Getenv("REX_SESSION"), block: os.Getenv("REX_BLOCK"), pointer: pointer}
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return fmt.Errorf("display requires a terminal")
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return err
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()
	// Watch the directory so atomic frame replacements keep producing events.
	if err = watcher.Add(filepath.Dir(path)); err != nil {
		return err
	}
	original, err := term.MakeRaw(fd)
	if err != nil {
		return err
	}
	defer term.Restore(fd, original)
	// In-band resize reports wake us even when SIGWINCH is delayed or absent.
	// Terminals without mode 2048 support ignore it; polling remains bounded.
	fmt.Print("\x1b[?1049h\x1b[?25l\x1b[?7l\x1b[?1002h\x1b[?1006h\x1b[?1016h\x1b[?2004h\x1b[?2048h")
	defer fmt.Print("\x1b[?2048l\x1b[?2026l\x1b[?2004l\x1b[?1016l\x1b[?1006l\x1b[?1002l\x1b[0m\x1b[?7h\x1b[?25h\x1b[?1049l")
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	resized := make(chan struct{}, 1)
	go func() {
		buf := make([]byte, 4096)
		mouse := mouseInput{}
		for {
			n, err := os.Stdin.Read(buf)
			if err != nil {
				cancel()
				return
			}
			events, interrupt := mouse.feed(buf[:n])
			if mouse.resized {
				select {
				case resized <- struct{}{}:
				default:
				}
			}
			if interrupt {
				cancel()
				return
			}
			if drag.session == "" || drag.block == "" {
				continue
			}
			for _, event := range events {
				if err := drag.event(ctx, event); err != nil {
					drag.active = false
				}
			}
		}
	}()
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)
	last := ""
	lastTarget := 0
	var gate frameGate
	retry := time.NewTimer(time.Hour)
	retry.Stop()
	defer retry.Stop()
	waiting := false
	lastColumns := -1
	lastRows := -1
	var traced string
	var received time.Time
	traceFinished := false
	draw := func() error {
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		data, err := io.ReadAll(io.LimitReader(file, 2<<20))
		file.Close()
		if err != nil {
			return err
		}
		if len(data) == 2<<20 {
			return fmt.Errorf("display frame too large")
		}
		columns, rows, err := term.GetSize(int(os.Stdout.Fd()))
		if err != nil {
			return err
		}
		var frame presentation.Frame
		if err := json.Unmarshal(data, &frame); err != nil {
			return fmt.Errorf("decode display frame: %w", err)
		}
		if frame.Columns < 1 {
			return fmt.Errorf("invalid display frame width")
		}
		if logger != nil && frame.Trace != nil && frame.Trace.ID != traced {
			traced, received, traceFinished = frame.Trace.ID, time.Now(), false
			logger.Record(*frame.Trace, frame.Trace.Window, "frame_received", time.Time{}, "columns", columns, "target_columns", frame.Columns)
		}
		raw := render.Sanitize(frame.Line)
		waiting = !gate.ready(frame.Columns, lastTarget, columns, lastColumns, time.Now())
		if waiting {
			retry.Reset(frameRetryInterval)
			return nil
		}
		retry.Stop()
		traceDraw := logger != nil && frame.Trace != nil && !traceFinished
		if traceDraw {
			logger.Record(*frame.Trace, frame.Trace.Window, "dimensions_ready", received, "timed_out", gate.timedOut, "columns", columns, "target_columns", frame.Columns)
		}
		drawBegan := time.Now()
		submitted := false
		line := render.Fit(raw, columns)
		if line != last || columns != lastColumns || rows != lastRows {
			clear := ""
			if columns != lastColumns || rows != lastRows {
				clear = "\x1b[H\x1b[2J"
			}
			if _, err = fmt.Fprintf(os.Stdout, "\x1b[?2026h%s\x1b[%d;1H\x1b[2K%s\x1b[0m\x1b[?2026l", clear, max(1, rows), line); err != nil {
				return err
			}
			submitted = true
			last = line
			lastColumns = columns
			lastRows = rows
		}
		if traceDraw {
			stage := "draw_skipped"
			if submitted {
				stage = "draw_submitted"
			}
			logger.Record(*frame.Trace, frame.Trace.Window, stage, drawBegan, "total_ms", float64(time.Since(frame.Trace.Started))/float64(time.Millisecond))
			traceFinished = true
		}
		lastTarget = frame.Columns
		return nil
	}
	if err = draw(); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-retry.C:
			if !waiting {
				continue
			}
		case <-winch:
			// Both resize sources recheck the actual PTY size through frameGate.
		case <-resized:
			// Never draw the reported dimensions before the PTY agrees.
		case event, ok := <-watcher.Events:
			if !ok {
				return fmt.Errorf("display frame watcher closed")
			}
			if event.Name != path || !event.Has(fsnotify.Create|fsnotify.Write) {
				continue
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return fmt.Errorf("display frame watcher closed")
			}
			return fmt.Errorf("watch display frame: %w", err)
		}
		if err = draw(); err != nil {
			return err
		}
	}
}
