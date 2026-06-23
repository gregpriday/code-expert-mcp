package mcpserver

import (
	"testing"

	"github.com/gregpriday/codeexpert/internal/config"
	"github.com/gregpriday/codeexpert/internal/telemetry"
	"github.com/gregpriday/codeexpert/internal/workflow"
)

// TestServerConstructs ensures the SDK can infer input/output schemas for the
// large request/result types without panicking at registration.
func TestServerConstructs(t *testing.T) {
	d := Deps{
		Engine:  &workflow.Engine{Cfg: config.Defaults(), Log: telemetry.Nop()},
		Config:  config.Defaults(),
		Logger:  telemetry.Nop(),
		Version: "test",
	}
	s := New(d)
	if s == nil {
		t.Fatal("expected a server")
	}
}
