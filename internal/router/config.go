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

	vaultpkg "github.com/Coder8124/brain/internal/vault"
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
	// RetentionDays is how long raw capture events are kept before the daemon
	// drops them. Rollups have already extracted anything worth keeping by then,
	// which is the whole point of the two-tier design.
	//
	// This existed as an idea long before it existed as a number: capture polls
	// every five seconds, and until now nothing scheduled a prune, so raw events
	// accumulated for as long as the daemon ran. 0 means the default; a negative
	// value means keep everything, for someone who has decided that on purpose.
	RetentionDays int `json:"retention_days,omitempty"`
}

// DefaultRetentionDays is long enough that a monthly consolidation still has its
// evidence, short enough that a laptop running the daemon continuously does not
// quietly fill up.
const DefaultRetentionDays = 30

// Retention resolves the configured window, in days. The second return says
// whether events are kept forever, which callers must report rather than
// present as a number.
func (c *Config) Retention() (days int, forever bool) {
	switch {
	case c == nil || c.RetentionDays == 0:
		return DefaultRetentionDays, false
	case c.RetentionDays < 0:
		return 0, true
	default:
		return c.RetentionDays, false
	}
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
	cfg.RetentionDays = onDisk.RetentionDays
	return cfg, nil
}

func (c *Config) Save(vault string) error {
	if err := vaultpkg.MkdirPrivate(filepath.Dir(ConfigPath(vault))); err != nil {
		return err
	}
	raw, err := jsonMarshalIndent(c)
	if err != nil {
		return err
	}
	return os.WriteFile(ConfigPath(vault), raw, 0o600)
}
