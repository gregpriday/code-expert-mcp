// Package mcpserver exposes CodeExpert's two read-only tools (and optional run
// resources) over MCP using the official Go SDK. stdout is reserved for MCP
// messages; all logging goes to stderr.
package mcpserver

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gregpriday/codeexpert/internal/config"
	"github.com/gregpriday/codeexpert/internal/telemetry"
	"github.com/gregpriday/codeexpert/internal/workflow"
)

const serverInstructions = `CodeExpert performs read-only repository planning, engineering help, and code review. It never edits the repository. Use codeexpert_plan for planning or help (mode="help") and codeexpert_review for Git changes. Results may be long-running and include structured evidence.`

// Deps are the dependencies the server needs.
type Deps struct {
	Engine  *workflow.Engine
	Config  config.Config
	Logger  *telemetry.Logger
	Version string
}

// New builds and configures the MCP server with the two tools and resources.
func New(d Deps) *mcp.Server {
	impl := &mcp.Implementation{
		Name:    "codeexpert",
		Title:   "CodeExpert",
		Version: d.Version,
	}
	opts := &mcp.ServerOptions{
		Instructions: serverInstructions,
		Logger:       d.Logger.Slog(),
	}
	s := mcp.NewServer(impl, opts)
	registerTools(s, d)
	registerResources(s, d)
	return s
}
