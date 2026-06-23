package security

import (
	"os"
	"strings"
)

// defaultEnvAllowlist is the minimal environment passed to child check
// processes. Secrets and developer credentials are intentionally excluded.
var defaultEnvAllowlist = []string{
	"PATH", "HOME", "TMPDIR", "TEMP", "TMP",
	"LANG", "LC_ALL", "LC_CTYPE",
	"SYSTEMROOT", "WINDIR", "PATHEXT", // Windows essentials
	"GOFLAGS", "GOPATH", "GOMODCACHE", "GOCACHE", "GOTOOLCHAIN",
}

// secretEnvSignatures cause an environment variable to be dropped even if it
// somehow appears on an allowlist.
var secretEnvSignatures = []string{
	"KEY", "SECRET", "TOKEN", "PASSWORD", "PASSWD", "CREDENTIAL", "AUTH",
}

// AllowlistedEnv returns a minimal child-process environment built only from
// variables on the allowlist (plus any extras), with anything matching a secret
// signature removed.
func AllowlistedEnv(extra ...string) []string {
	allow := map[string]bool{}
	for _, k := range defaultEnvAllowlist {
		allow[k] = true
	}
	for _, k := range extra {
		allow[strings.ToUpper(k)] = true
	}
	var out []string
	for _, kv := range os.Environ() {
		i := strings.IndexByte(kv, '=')
		if i < 0 {
			continue
		}
		key := kv[:i]
		if !allow[strings.ToUpper(key)] {
			continue
		}
		if looksSecret(key) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

func looksSecret(key string) bool {
	upper := strings.ToUpper(key)
	for _, sig := range secretEnvSignatures {
		if strings.Contains(upper, sig) {
			return true
		}
	}
	return false
}
