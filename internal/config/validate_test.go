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

func TestValidateRejectsUnsupportedVersion(t *testing.T) {
	c := Defaults()
	c.Version = 99
	if err := Validate(&c); err == nil {
		t.Fatal("expected unsupported-version rejection")
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
