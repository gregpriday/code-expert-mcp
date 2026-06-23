// Package telemetry provides structured logging (and hooks for tracing and
// metrics). All logs go to stderr so stdout stays reserved for MCP messages.
// Prompts, file contents, API keys, and raw provider bodies are never logged by
// default.
package telemetry

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
)

// Logger wraps slog with CodeExpert conventions.
type Logger struct {
	sl *slog.Logger
}

// Options configures the logger.
type Options struct {
	Level string    // debug, info, warn, error
	JSON  bool      // JSON output instead of text
	Out   io.Writer // defaults to os.Stderr
}

// New constructs a Logger writing to stderr (or Options.Out).
func New(opts Options) *Logger {
	out := opts.Out
	if out == nil {
		out = os.Stderr
	}
	lvl := parseLevel(opts.Level)
	var h slog.Handler
	ho := &slog.HandlerOptions{Level: lvl}
	if opts.JSON {
		h = slog.NewJSONHandler(out, ho)
	} else {
		h = slog.NewTextHandler(out, ho)
	}
	return &Logger{sl: slog.New(h)}
}

// Nop returns a logger that discards everything.
func Nop() *Logger {
	return &Logger{sl: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// Slog exposes the underlying slog.Logger for libraries that want one.
func (l *Logger) Slog() *slog.Logger { return l.sl }

// With returns a child logger with the given key/value attributes.
func (l *Logger) With(args ...any) *Logger {
	return &Logger{sl: l.sl.With(args...)}
}

func (l *Logger) Debug(msg string, args ...any) { l.sl.Debug(msg, args...) }
func (l *Logger) Info(msg string, args ...any)  { l.sl.Info(msg, args...) }
func (l *Logger) Warn(msg string, args ...any)  { l.sl.Warn(msg, args...) }
func (l *Logger) Error(msg string, args ...any) { l.sl.Error(msg, args...) }

type ctxKey struct{}

// IntoContext stores a logger in ctx.
func IntoContext(ctx context.Context, l *Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

// FromContext retrieves a logger from ctx, falling back to a Nop logger.
func FromContext(ctx context.Context) *Logger {
	if l, ok := ctx.Value(ctxKey{}).(*Logger); ok && l != nil {
		return l
	}
	return Nop()
}
