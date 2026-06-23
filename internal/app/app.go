// Package app wires the configuration, logger, provider, cache, and workflow
// engine into a single application context shared by the CLI and MCP server.
package app

import (
	"github.com/gregpriday/codeexpert/internal/cache"
	"github.com/gregpriday/codeexpert/internal/config"
	"github.com/gregpriday/codeexpert/internal/provider"
	"github.com/gregpriday/codeexpert/internal/provider/openaicompat"
	"github.com/gregpriday/codeexpert/internal/repo"
	"github.com/gregpriday/codeexpert/internal/schema"
	"github.com/gregpriday/codeexpert/internal/telemetry"
	"github.com/gregpriday/codeexpert/internal/workflow"
)

// Version is the application version, overridable at build time via the CLI.
var Version = "0.1.0-dev"

// App holds the resolved runtime context.
type App struct {
	Config   config.Config
	Sources  []string
	Logger   *telemetry.Logger
	Provider provider.Provider
	Cache    *cache.Cache
	Engine   *workflow.Engine
	// Root is the effective repository root resolved from the explicit flag,
	// CLAUDE_PROJECT_DIR, or the working directory. ProjectFile is the project
	// config file that was loaded, or "" if none was found.
	Root        string
	ProjectFile string
}

// BuildOptions tune application construction.
type BuildOptions struct {
	Root         string
	LogLevel     string // overrides config
	LogJSON      bool
	Quiet        bool
	DisableCache bool
}

// Build resolves configuration and constructs the application. Provider calls
// are deferred; a missing API key is not fatal here (it surfaces at call time).
//
// The effective repository root is resolved BEFORE configuration is loaded so a
// project .codeexpert.toml is discovered even when no --root is passed (it then
// comes from CLAUDE_PROJECT_DIR or the working directory). This fixes the case
// where project config was silently skipped for the common default-root path.
func Build(opts BuildOptions) (*App, error) {
	effRoot := repo.DefaultRoot(opts.Root)
	lr, err := config.Load(effRoot)
	if err != nil {
		return nil, err
	}
	cfg := lr.Config
	if opts.LogLevel != "" {
		cfg.Server.LogLevel = opts.LogLevel
	}
	if opts.DisableCache {
		cfg.Cache.Enabled = false
	}
	if err := config.Validate(&cfg); err != nil {
		return nil, err
	}

	level := cfg.Server.LogLevel
	if opts.Quiet {
		level = "error"
	}
	log := telemetry.New(telemetry.Options{Level: level, JSON: cfg.Server.LogJSON || opts.LogJSON})

	c, err := cache.Open(cfg.Cache)
	if err != nil {
		return nil, err
	}

	prov, err := openaicompat.New(openaicompat.Options{
		BaseURL:           cfg.Provider.BaseURL,
		APIKey:            cfg.Provider.APIKey,
		Dialect:           cfg.Provider.API,
		ConnectTimeout:    cfg.Provider.ConnectTimeout.Std(),
		RequestTimeout:    cfg.Provider.RequestTimeout.Std(),
		StreamIdleTimeout: cfg.Provider.StreamIdleTimeout.Std(),
		MaxRetries:        cfg.Provider.MaxRetries,
		UserAgent:         "codeexpert/" + Version,
		Logger:            log,
	})
	if err != nil {
		return nil, err
	}

	eng := &workflow.Engine{Cfg: cfg, Provider: prov, Cache: c, Log: log}

	return &App{
		Config:      cfg,
		Sources:     lr.SourcesNoted,
		Logger:      log,
		Provider:    prov,
		Cache:       c,
		Engine:      eng,
		Root:        effRoot,
		ProjectFile: lr.ProjectFile,
	}, nil
}

// RequireAPIKey returns a typed error if no provider key is configured.
func (a *App) RequireAPIKey() error {
	if a.Config.Provider.APIKey == "" {
		return schema.NewError(schema.CodeProviderAuth,
			"no API key found; set the %s environment variable", a.Config.Provider.APIKeyEnv)
	}
	return nil
}
