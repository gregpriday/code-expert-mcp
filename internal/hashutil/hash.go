// Package hashutil provides the single content-hashing primitive used for cache
// keys, blob identity, and snapshot IDs. SHA-256 is used so the default binary
// stays CGO-free; the function is centralized so it can be swapped for BLAKE3
// behind a build tag without touching callers.
package hashutil

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"sort"
	"strings"
)

// Hash returns the lowercase hex SHA-256 of b.
func Hash(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// HashString hashes a string.
func HashString(s string) string { return Hash([]byte(s)) }

// HashReader streams r through SHA-256.
func HashReader(r io.Reader) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Short returns the first n hex characters of a hash for human-readable IDs.
func Short(hash string, n int) string {
	if n <= 0 || n > len(hash) {
		return hash
	}
	return hash[:n]
}

// HashFields combines an ordered set of labeled fields into one stable hash. The
// labels guard against field-boundary ambiguity.
func HashFields(fields map[string]string) string {
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(fields[k])
		b.WriteByte('\n')
	}
	return HashString(b.String())
}

// HashList hashes an ordered list of strings (order-sensitive).
func HashList(items []string) string {
	return HashString(strings.Join(items, "\x00"))
}
