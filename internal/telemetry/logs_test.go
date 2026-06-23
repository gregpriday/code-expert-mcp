package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

// parseLines decodes each JSON log line emitted into buf.
func parseLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("log line is not valid JSON %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

func TestLevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	log := New(Options{Level: "info", JSON: true, Out: &buf})

	log.Debug("debug-message")
	log.Info("info-message")
	log.Warn("warn-message")
	log.Error("error-message")

	lines := parseLines(t, &buf)
	msgs := map[string]bool{}
	for _, l := range lines {
		if m, ok := l["msg"].(string); ok {
			msgs[m] = true
		}
	}
	if msgs["debug-message"] {
		t.Error("debug message must be filtered out at info level")
	}
	for _, want := range []string{"info-message", "warn-message", "error-message"} {
		if !msgs[want] {
			t.Errorf("%q should be logged at info level", want)
		}
	}
}

func TestDebugLevelEmitsDebug(t *testing.T) {
	var buf bytes.Buffer
	log := New(Options{Level: "debug", JSON: true, Out: &buf})
	log.Debug("hello-debug")
	if !strings.Contains(buf.String(), "hello-debug") {
		t.Errorf("debug message should appear at debug level:\n%s", buf.String())
	}
}

func TestWithPropagation(t *testing.T) {
	var buf bytes.Buffer
	log := New(Options{Level: "info", JSON: true, Out: &buf}).With("request_id", "abc-123")
	log.Info("handling")

	lines := parseLines(t, &buf)
	if len(lines) != 1 {
		t.Fatalf("expected 1 log line, got %d", len(lines))
	}
	if got, _ := lines[0]["request_id"].(string); got != "abc-123" {
		t.Errorf("With() attribute not propagated: request_id = %q, want abc-123", got)
	}
}

func TestContextRoundTrip(t *testing.T) {
	log := New(Options{JSON: true})
	ctx := IntoContext(context.Background(), log)
	if got := FromContext(ctx); got != log {
		t.Errorf("FromContext should return the stored logger")
	}
	// An empty context yields a non-nil (Nop) logger, not a panic.
	if got := FromContext(context.Background()); got == nil {
		t.Error("FromContext on an empty context must return a non-nil logger")
	}
}

func TestNopDoesNotPanic(t *testing.T) {
	log := Nop()
	if log == nil || log.Slog() == nil {
		t.Fatal("Nop must return a usable logger")
	}
	// These must be safe no-ops (output discarded).
	log.Debug("x")
	log.Info("y")
	log.Warn("z")
	log.Error("w")
	log.With("k", "v").Info("child")
}

func TestParseLevel(t *testing.T) {
	cases := map[string]slog.Level{
		"debug":   slog.LevelDebug,
		"DEBUG":   slog.LevelDebug,
		" info ":  slog.LevelInfo,
		"warn":    slog.LevelWarn,
		"warning": slog.LevelWarn,
		"error":   slog.LevelError,
		"":        slog.LevelInfo,
		"bogus":   slog.LevelInfo,
	}
	for in, want := range cases {
		if got := parseLevel(in); got != want {
			t.Errorf("parseLevel(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestNewDefaultsToInfoTextWithoutOptions(t *testing.T) {
	// New with zero Options must not panic and must produce a working logger
	// (output goes to stderr by default; we only assert construction here).
	log := New(Options{})
	if log == nil || log.Slog() == nil {
		t.Fatal("New(Options{}) must return a usable logger")
	}
}
