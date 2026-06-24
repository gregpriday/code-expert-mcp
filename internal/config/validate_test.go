package config

import (
	"testing"
	"time"
)

func TestValidateDefaults(t *testing.T) {
	c := Defaults()
	// Localhost http requires explicit opt-in; the Sakana default is https so OK.
	if err := Validate(&c); err != nil {
		t.Fatalf("defaults should validate: %v", err)
	}
}

func TestValidateRejectsShellMetachars(t *testing.T) {
	c := Defaults()
	c.Checks.Command = []CheckCommand{{Name: "evil", Argv: []string{"sh", "-c", "rm -rf / && echo $HOME"}}}
	if err := Validate(&c); err == nil {
		t.Fatal("expected rejection of shell metacharacters in check argv")
	}
}

func TestValidateRejectsInsecureRemoteHTTP(t *testing.T) {
	c := Defaults()
	c.Provider.BaseURL = "http://example.com/v1"
	if err := Validate(&c); err == nil {
		t.Fatal("expected rejection of insecure http for non-local host")
	}
}

// TestValidateRemoteHTTPRejectedEvenWithOptIn proves the loopback opt-in cannot
// widen the trust boundary to a remote host (P0-12), including a hostname that
// merely starts with "127.".
func TestValidateRemoteHTTPRejectedEvenWithOptIn(t *testing.T) {
	for _, host := range []string{"http://example.com/v1", "http://127.example.com/v1", "http://127.0.0.1.evil.com/v1"} {
		c := Defaults()
		c.Provider.BaseURL = host
		c.Provider.AllowInsecureHTTPLocalhost = true // must NOT allow a remote http host
		if err := Validate(&c); err == nil {
			t.Errorf("expected rejection of insecure http for %q even with opt-in", host)
		}
	}
}

// TestValidateAcceptsLoopbackHTTPWithOptIn proves real loopback http is permitted
// with the explicit opt-in.
func TestValidateAcceptsLoopbackHTTPWithOptIn(t *testing.T) {
	for _, host := range []string{"http://127.0.0.1:1234/v1", "http://localhost:1234/v1", "http://[::1]:1234/v1"} {
		c := Defaults()
		c.Provider.BaseURL = host
		c.Provider.AllowInsecureHTTPLocalhost = true
		if err := Validate(&c); err != nil {
			t.Errorf("loopback http with opt-in should validate (%s): %v", host, err)
		}
	}
}

func TestValidateRejectsUnsupportedVersion(t *testing.T) {
	c := Defaults()
	c.Version = 99
	if err := Validate(&c); err == nil {
		t.Fatal("expected unsupported-version rejection")
	}
}

func TestValidateRejectsBadSearchContextSize(t *testing.T) {
	c := Defaults()
	c.Grounding.Enabled = true
	c.Grounding.SearchContextSize = "huge"
	if err := Validate(&c); err == nil {
		t.Fatal("expected rejection of an invalid grounding.search_context_size")
	}
}

func TestValidateRejectsDomainWithScheme(t *testing.T) {
	c := Defaults()
	c.Grounding.AllowedDomains = []string{"https://go.dev"}
	if err := Validate(&c); err == nil {
		t.Fatal("expected rejection of a domain filter carrying a scheme")
	}
}

func TestValidateRejectsDomainWithPath(t *testing.T) {
	c := Defaults()
	c.Grounding.BlockedDomains = []string{"go.dev/doc"}
	if err := Validate(&c); err == nil {
		t.Fatal("expected rejection of a domain filter carrying a path")
	}
}

func TestValidateAcceptsBareDomains(t *testing.T) {
	c := Defaults()
	c.Grounding.Enabled = true
	c.Grounding.SearchContextSize = "high"
	c.Grounding.AllowedDomains = []string{"go.dev", "pkg.go.dev"}
	c.Grounding.BlockedDomains = []string{"spam.example"}
	if err := Validate(&c); err != nil {
		t.Fatalf("bare domains should validate: %v", err)
	}
}

func TestValidateRejectsTooManyDomains(t *testing.T) {
	c := Defaults()
	domains := make([]string, 101)
	for i := range domains {
		domains[i] = "d.example"
	}
	c.Grounding.AllowedDomains = domains
	if err := Validate(&c); err == nil {
		t.Fatal("expected rejection of more than 100 domain filters")
	}
}

func TestDurationParsesDays(t *testing.T) {
	var d Duration
	if err := d.UnmarshalText([]byte("7d")); err != nil {
		t.Fatal(err)
	}
	if d.Std() != 7*24*time.Hour {
		t.Errorf("7d = %v, want 168h", d.Std())
	}
}
