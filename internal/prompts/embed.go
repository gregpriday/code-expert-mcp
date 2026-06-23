// Package prompts embeds the versioned prompt assets with stable IDs and content
// hashes. Each run records the prompt IDs and hashes it used so outputs are
// reproducible and auditable.
package prompts

import (
	"bytes"
	"embed"
	"io/fs"
	"strings"
	"sync"
	"text/template"

	"github.com/gregpriday/codeexpert/internal/hashutil"
)

//go:embed assets/*.md
var assets embed.FS

// Stable prompt IDs.
const (
	CommonSystem     = "common-system"
	PlanExplore      = "plan-explore"
	PlanFinalize     = "plan-finalize"
	HelpFinalize     = "help-finalize"
	ReviewDiffLocal  = "review-diff-local"
	ReviewContext    = "review-context"
	ReviewSpecialist = "review-specialist"
	ReviewVerify     = "review-verify"
	ReviewFinalize   = "review-finalize"
	RepairSchema     = "repair-schema"
)

var (
	cacheMu   sync.RWMutex
	hashCache = map[string]string{}
)

// Get returns the raw prompt content for an ID.
func Get(id string) (string, error) {
	b, err := assets.ReadFile("assets/" + id + ".md")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// MustGet returns the prompt content or panics (used for embedded, always-present
// assets at startup).
func MustGet(id string) string {
	s, err := Get(id)
	if err != nil {
		panic("prompts: missing asset " + id + ": " + err.Error())
	}
	return s
}

// Hash returns the content hash (short) of a prompt, for provenance.
func Hash(id string) string {
	cacheMu.RLock()
	if h, ok := hashCache[id]; ok {
		cacheMu.RUnlock()
		return h
	}
	cacheMu.RUnlock()
	content, err := Get(id)
	if err != nil {
		return ""
	}
	h := hashutil.Short(hashutil.HashString(content), 12)
	cacheMu.Lock()
	hashCache[id] = h
	cacheMu.Unlock()
	return h
}

// Render executes a prompt as a text/template with the given data.
func Render(id string, data any) (string, error) {
	content, err := Get(id)
	if err != nil {
		return "", err
	}
	tmpl, err := template.New(id).Option("missingkey=zero").Parse(content)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// List returns all embedded prompt IDs.
func List() []string {
	var ids []string
	_ = fs.WalkDir(assets, "assets", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		name := strings.TrimSuffix(strings.TrimPrefix(p, "assets/"), ".md")
		ids = append(ids, name)
		return nil
	})
	return ids
}
