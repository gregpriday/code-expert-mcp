package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const projectCfg = `version = 2
[provider]
active = "sakana"
[providers.sakana]
api = "responses"
base_url = "https://api.sakana.ai/v1"
api_key_env = "SAKANA_API_KEY"
small_model = "fugu"
large_model = "fugu-ultra"
`

// TestEffectiveRootLoadsProjectConfigFromCwd proves the P0-8 fix: with no --root,
// app.Build resolves the working directory and discovers its .codeexpert.toml.
func TestEffectiveRootLoadsProjectConfigFromCwd(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".codeexpert.toml"), []byte(projectCfg), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	a, err := Build(BuildOptions{Root: "", DisableCache: true})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if a.Config.Provider.Active != "sakana" {
		t.Errorf("project config not loaded from cwd: active=%q", a.Config.Provider.Active)
	}
	if a.ProjectFile == "" || !strings.HasSuffix(a.ProjectFile, ".codeexpert.toml") {
		t.Errorf("ProjectFile not reported: %q", a.ProjectFile)
	}
	if a.Root == "" {
		t.Errorf("effective root not reported")
	}
}

// TestEffectiveRootExplicit proves an explicit root is honored for discovery.
func TestEffectiveRootExplicit(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".codeexpert.toml"), []byte(projectCfg), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := Build(BuildOptions{Root: dir, DisableCache: true})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if a.Config.Provider.Active != "sakana" || a.ProjectFile == "" {
		t.Errorf("explicit root did not load project config: active=%q file=%q", a.Config.Provider.Active, a.ProjectFile)
	}
}
