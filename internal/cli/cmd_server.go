package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/gregpriday/codeexpert/internal/app"
	"github.com/gregpriday/codeexpert/internal/mcpserver"
)

func cmdMCP(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	transport := fs.String("transport", "stdio", "stdio (only stdio is supported for `mcp`; use `serve` for HTTP)")
	root := fs.String("root", "", "default repository root")
	if err := fs.Parse(args); err != nil {
		return exitInvalidArgs
	}
	if *transport != "stdio" {
		fmt.Fprintln(os.Stderr, "the mcp command only supports --transport stdio; use `codeexpert serve` for Streamable HTTP")
		return exitInvalidArgs
	}
	if *root != "" {
		_ = os.Setenv("CLAUDE_PROJECT_DIR", *root)
	}

	a, err := app.Build(app.BuildOptions{Root: *root})
	if err != nil {
		return fail(err)
	}
	// stdout is reserved for MCP; the logger already writes to stderr.
	a.Logger.Info("starting MCP server (stdio)", "version", app.Version)

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := mcpserver.RunStdio(ctx, mcpserver.Deps{
		Engine:  a.Engine,
		Config:  a.Config,
		Logger:  a.Logger,
		Version: app.Version,
	}); err != nil {
		if ctx.Err() != nil {
			return exitOK
		}
		return fail(err)
	}
	return exitOK
}

func cmdServe(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	listen := fs.String("listen", "", "address to bind (default from config, 127.0.0.1:7341)")
	transport := fs.String("transport", "streamable-http", "streamable-http")
	root := fs.String("root", "", "default repository root")
	if err := fs.Parse(args); err != nil {
		return exitInvalidArgs
	}
	if *transport != "streamable-http" {
		fmt.Fprintln(os.Stderr, "serve only supports --transport streamable-http")
		return exitInvalidArgs
	}
	if *root != "" {
		_ = os.Setenv("CLAUDE_PROJECT_DIR", *root)
	}

	a, err := app.Build(app.BuildOptions{Root: *root})
	if err != nil {
		return fail(err)
	}
	addr := *listen
	if addr == "" {
		addr = a.Config.Server.Listen
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	err = mcpserver.RunStreamableHTTP(ctx, mcpserver.Deps{
		Engine:  a.Engine,
		Config:  a.Config,
		Logger:  a.Logger,
		Version: app.Version,
	}, mcpserver.HTTPConfig{
		Listen:          addr,
		AllowedOrigins:  a.Config.Server.AllowedOrigins,
		AuthToken:       a.Config.Server.AuthToken,
		MaxRequestBytes: a.Config.Server.MaxRequestBytes,
	})
	if err != nil {
		return fail(err)
	}
	return exitOK
}
