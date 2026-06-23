package llmtools

import (
	"strconv"
	"strings"
)

func itoa(n int) string { return strconv.Itoa(n) }

// sliceLines extracts the inclusive 1-based [start,end] line range from content,
// clamping to the file bounds. It returns the joined text and the actual range.
func sliceLines(content []byte, start, end int) (string, int, int) {
	lines := strings.Split(string(content), "\n")
	n := len(lines)
	if start < 1 {
		start = 1
	}
	if start > n {
		start = n
	}
	if end < start {
		end = start
	}
	if end > n {
		end = n
	}
	// Prefix each line with its number for unambiguous citation by the model.
	var b strings.Builder
	for i := start - 1; i < end && i < n; i++ {
		b.WriteString(strconv.Itoa(i + 1))
		b.WriteByte('\t')
		b.WriteString(lines[i])
		b.WriteByte('\n')
	}
	return b.String(), start, end
}
