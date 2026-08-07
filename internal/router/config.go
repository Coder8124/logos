// Package router decides which model does which job.
//
// The provider package speaks one dialect to every runtime; this package is the
// policy on top: which tier handles a task, what happens when a tier is
// missing, and what may cross the network.
package router

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Tier orders jobs by how much capability they need. Small local models are
// perfectly good at narrow extraction and hopeless at synthesis, so the work is
// split rather than pointed at one model.
type Tier int

const (
	// T0 embeddings.
	T0 Tier = iota
	// T1 per-event classify and extract.
	T1
	// T2 rollup, entity resolution, interactive ask.
	T2
	// T3 weekly synthesis and hard queries. Cloud, opt-in.
	T3
)

func (t Tier) String() string {
	return [...]string{"T0", "T1", "T2", "T3"}[t]
}

func ParseTier(s string) (Tier, error) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "T0":
		return T0, nil
	case "T1":
		return T1, nil
	case "T2":
		return T2, nil
	case "T3":
		return T3, nil
	}
	return T0, fmt.Errorf("unknown tier %q", s)
}

// TierConfig binds a tier to a model and, for T3, to a remote endpoint.
type TierConfig struct {
	Model string `json:"model"`
	// BaseURL empty means "use the discovered local runtime".
	BaseURL string `json:"base_url,omitempty"`
	// KeyRef names a Keychain entry. Keys are never stored in the config file
	// and never in the vault.
	KeyRef string `json:"key_ref,omitempty"`
	// CloudOK gates network egress for this tier. Off until the user confirms
	// a redaction preview.
	CloudOK bool `json:"cloud_ok,omitempty"`
}

type Config struct {
	Tiers map[string]TierConfig `json:"tiers"`
	// ExtraBlocked apps appended to the capture blocklist.
	ExtraBlocked []string `json:"extra_blocked,omitempty"`
	// Think is how much a reasoning model reasons before answering: "off", "low",
	// "medium", or "high". Empty means "low". This is the knob that keeps a
	// thinking model from spending its whole budget thinking and returning an
	// empty answer over the /v1 endpoint.
	Think string `json:"think,omitempty"`
}

// Defaults match what a stock Ollama install tends to have. Anything missing is
// resolved at probe time rather than failing here.
func Defaults() *Config {
	return &Config{
		Tiers: map[string]TierConfig{
			"T0": {Model: "nomic-embed-text"},
			"T1": {Model: "gemma3:4b"},
			"T2": {Model: "qwen3.6"},
			"T3": {Model: "claude-opus-4-8", BaseURL: "https://api.anthropic.com/v1", KeyRef: "brain-anthropic"},
		},
		Think: "low",
	}
}

func ConfigPath(vault string) string {
	return filepath.Join(vault, ".brain", "config.json")
}

func Load(vault string) (*Config, error) {
	cfg := Defaults()

	raw, err := os.ReadFile(ConfigPath(vault))
	if os.IsNotExist(err) {
		return cfg, nil // absent config is not an error; defaults are usable
	}
	if err != nil {
		return nil, err
	}

	var onDisk Config
	if err := jsonUnmarshal(raw, &onDisk); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", ConfigPath(vault), err)
	}
	// Merge rather than replace, so a config naming only T2 keeps the rest.
	for name, tc := range onDisk.Tiers {
		cfg.Tiers[name] = tc
	}
	cfg.ExtraBlocked = onDisk.ExtraBlocked
	if onDisk.Think != "" {
		cfg.Think = onDisk.Think
	}
	return cfg, nil
}

func (c *Config) Save(vault string) error {
	if err := os.MkdirAll(filepath.Dir(ConfigPath(vault)), 0o755); err != nil {
		return err
	}
	raw, err := jsonMarshalIndent(c)
	if err != nil {
		return err
	}
	return os.WriteFile(ConfigPath(vault), raw, 0o600)
}
