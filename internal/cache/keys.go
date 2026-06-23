package cache

import "github.com/gregpriday/codeexpert/internal/hashutil"

// SchemaVersion is bumped when cached record formats change incompatibly.
const SchemaVersion = "1"

// Key builds a stable cache key from all inputs that can affect an output. Any
// change in snapshot, request, config, prompt, model, provider, or version
// invalidates the key.
type Key struct {
	Kind           string
	SnapshotID     string
	RequestHash    string
	ConfigHash     string
	PromptVersion  string
	ModelID        string
	ProviderOrigin string
	IndexerVersion string
}

// String renders the key as a content hash with a readable prefix.
func (k Key) String() string {
	digest := hashutil.HashFields(map[string]string{
		"kind":     k.Kind,
		"snapshot": k.SnapshotID,
		"request":  k.RequestHash,
		"config":   k.ConfigHash,
		"prompt":   k.PromptVersion,
		"model":    k.ModelID,
		"origin":   k.ProviderOrigin,
		"indexer":  k.IndexerVersion,
		"schema":   SchemaVersion,
	})
	return k.Kind + "-" + hashutil.Short(digest, 24)
}
