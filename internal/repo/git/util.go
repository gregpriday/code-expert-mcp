package git

import (
	"strconv"
	"strings"
)

func itoa(n int) string { return strconv.Itoa(n) }

func splitLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}

func splitUnit(s string) []string { return strings.Split(s, "\x1f") }
