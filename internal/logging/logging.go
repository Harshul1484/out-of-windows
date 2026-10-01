// Package logging configures the process-wide slog logger.
//
// Logs go to <data>\logs\oow.log as JSON lines (rotated at 5 MB, one backup).
// With --debug, human-readable debug output is also written to stderr.
// Logging never writes to stdout, so it cannot corrupt --json output.
package logging

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

const maxLogSize = 5 << 20

// Setup installs the default logger and returns a function that flushes and
// closes the log file, reporting any error. Failure to open the log file is
// not fatal.
func Setup(logDir string, debug bool, stderr io.Writer) func() error {
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}
	var handlers []slog.Handler
	var file *os.File

	if logDir != "" {
		if f, err := openLog(logDir); err == nil {
			file = f
			handlers = append(handlers, slog.NewJSONHandler(f, &slog.HandlerOptions{Level: level}))
		}
	}
	if debug && stderr != nil {
		handlers = append(handlers, slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	slog.SetDefault(slog.New(fanout(handlers)))
	return func() error {
		if file == nil {
			return nil
		}
		return errors.Join(file.Sync(), file.Close())
	}
}

func openLog(dir string) (*os.File, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	p := filepath.Join(dir, "oow.log")
	if info, err := os.Stat(p); err == nil && info.Size() > maxLogSize {
		_ = os.Remove(p + ".1")
		_ = os.Rename(p, p+".1")
	}
	return os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
}

type multiHandler []slog.Handler

func fanout(hs []slog.Handler) slog.Handler {
	if len(hs) == 0 {
		return slog.NewTextHandler(io.Discard, nil)
	}
	if len(hs) == 1 {
		return hs[0]
	}
	return multiHandler(hs)
}

func (m multiHandler) Enabled(ctx context.Context, l slog.Level) bool {
	for _, h := range m {
		if h.Enabled(ctx, l) {
			return true
		}
	}
	return false
}

func (m multiHandler) Handle(ctx context.Context, r slog.Record) error {
	for _, h := range m {
		if h.Enabled(ctx, r.Level) {
			_ = h.Handle(ctx, r.Clone())
		}
	}
	return nil
}

func (m multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make(multiHandler, len(m))
	for i, h := range m {
		out[i] = h.WithAttrs(attrs)
	}
	return out
}

func (m multiHandler) WithGroup(name string) slog.Handler {
	out := make(multiHandler, len(m))
	for i, h := range m {
		out[i] = h.WithGroup(name)
	}
	return out
}
