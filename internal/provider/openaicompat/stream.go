package openaicompat

import (
	"bufio"
	"io"
	"strings"
	"sync"
	"time"
)

// idleReader wraps a streaming response body and aborts it if no data arrives for
// the idle timeout, so a stalled SSE stream cannot hang a run indefinitely. The
// timer resets on every read that makes progress; a fully stalled read trips it.
// A zero timeout disables the watchdog (callers should not wrap in that case).
type idleReader struct {
	r        io.ReadCloser
	d        time.Duration
	timer    *time.Timer
	mu       sync.Mutex
	timedOut bool
}

func newIdleReader(r io.ReadCloser, d time.Duration) *idleReader {
	ir := &idleReader{r: r, d: d}
	ir.timer = time.AfterFunc(d, func() {
		ir.mu.Lock()
		ir.timedOut = true
		ir.mu.Unlock()
		_ = ir.r.Close() // unblock a stalled Read
	})
	return ir
}

func (ir *idleReader) Read(p []byte) (int, error) {
	n, err := ir.r.Read(p)
	if n > 0 {
		ir.timer.Reset(ir.d)
	}
	if err != nil {
		ir.mu.Lock()
		to := ir.timedOut
		ir.mu.Unlock()
		if to {
			return n, errStreamIdle
		}
	}
	return n, err
}

// Stop halts the watchdog timer. Safe to call multiple times.
func (ir *idleReader) Stop() { ir.timer.Stop() }

// errStreamIdle marks an aborted stalled stream.
var errStreamIdle = idleErr("stream idle timeout exceeded")

type idleErr string

func (e idleErr) Error() string { return string(e) }

// sseEvent is one Server-Sent Event with an optional type and a data payload.
type sseEvent struct {
	Type string
	Data string
}

// scanSSE reads SSE events from r and calls fn for each complete event. It stops
// at EOF or when fn returns false. A larger buffer accommodates big response
// payloads delivered in a single data frame.
func scanSSE(r io.Reader, fn func(sseEvent) bool) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	var (
		evType string
		data   strings.Builder
	)
	flush := func() bool {
		if data.Len() == 0 && evType == "" {
			return true
		}
		ev := sseEvent{Type: evType, Data: data.String()}
		evType = ""
		data.Reset()
		return fn(ev)
	}
	for sc.Scan() {
		line := sc.Text()
		if line == "" { // event boundary
			if !flush() {
				return nil
			}
			continue
		}
		if strings.HasPrefix(line, ":") { // comment / heartbeat
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			evType = value
		case "data":
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(value)
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	flush()
	return nil
}
