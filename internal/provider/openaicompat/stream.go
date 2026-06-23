package openaicompat

import (
	"bufio"
	"io"
	"strings"
)

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
