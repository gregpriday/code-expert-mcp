package config

import (
	"time"
)

// Duration is a time.Duration that marshals to/from Go duration strings in TOML
// (e.g. "30m", "7d"). It also accepts a bare day suffix which Go's parser does
// not understand natively.
type Duration time.Duration

// UnmarshalText implements encoding.TextUnmarshaler for TOML decoding.
func (d *Duration) UnmarshalText(text []byte) error {
	v, err := parseFlexibleDuration(string(text))
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

// MarshalText implements encoding.TextMarshaler.
func (d Duration) MarshalText() ([]byte, error) {
	return []byte(time.Duration(d).String()), nil
}

// Std returns the standard library Duration.
func (d Duration) Std() time.Duration { return time.Duration(d) }

// parseFlexibleDuration extends time.ParseDuration with a "d" (days) suffix,
// which is common in retention/TTL config.
func parseFlexibleDuration(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	// Handle a trailing day suffix like "7d" or "30d".
	if n := len(s); n >= 2 && s[n-1] == 'd' && isAllDigits(s[:n-1]) {
		days := 0
		for _, c := range s[:n-1] {
			days = days*10 + int(c-'0')
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
