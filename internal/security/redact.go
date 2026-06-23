package security

import "regexp"

// secretPatterns are conservative signatures for common credential shapes. They
// are applied to logs and check output before persistence. They never see the
// API key configured for the provider (that is read directly from the env).
var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(api[_-]?key|secret|token|password|passwd|bearer)\s*[:=]\s*['"]?[A-Za-z0-9._\-]{12,}`),
	regexp.MustCompile(`sk-[A-Za-z0-9]{16,}`),
	regexp.MustCompile(`xox[baprs]-[A-Za-z0-9-]{10,}`),
	regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),
	regexp.MustCompile(`eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`), // JWT
}

const redactedMarker = "[REDACTED]"

// Redact replaces anything matching a known secret signature with a marker.
func Redact(s string) string {
	for _, re := range secretPatterns {
		s = re.ReplaceAllString(s, redactedMarker)
	}
	return s
}

// RedactBytes is the []byte form of Redact for check output streams.
func RedactBytes(b []byte) []byte {
	return []byte(Redact(string(b)))
}

// AddSecretValues registers exact literal values (e.g. a configured secret) to
// scrub from output, returning a redactor closure. The literals themselves are
// never logged.
func AddSecretValues(values ...string) func(string) string {
	literals := make([]string, 0, len(values))
	for _, v := range values {
		if len(v) >= 6 {
			literals = append(literals, v)
		}
	}
	return func(s string) string {
		s = Redact(s)
		for _, v := range literals {
			s = replaceAll(s, v, redactedMarker)
		}
		return s
	}
}

func replaceAll(s, old, new string) string {
	if old == "" {
		return s
	}
	return regexp.MustCompile(regexp.QuoteMeta(old)).ReplaceAllString(s, new)
}
