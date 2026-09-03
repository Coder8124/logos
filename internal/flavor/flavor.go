// Package flavor holds the assistant's identity and how present it makes itself.
//
// It was once the persona layer — secretary, tutor, business — with one engine
// shared between them. The verticals are gone. What remains is the small amount
// of state that makes the assistant feel like a particular assistant: what it
// calls you, what you call it, and how forward it is about speaking up.
package flavor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	vaultpkg "github.com/Coder8124/brain/internal/vault"
)

// Config is the assistant's slice of the vault's .brain directory. Kept in its
// own file rather than folded into the model router config, so identity settings
// never risk disturbing model or key settings.
type Config struct {
	// Name is what the user calls the assistant — how they address it and, when a
	// wake-word model is present, the word that wakes it. Empty means unnamed.
	Name string `json:"name,omitempty"`
	// UserName is what the assistant calls the user — used to warm the greeting
	// ("Morning, Pragun"). Empty means it stays impersonal.
	UserName string `json:"user_name,omitempty"`
	// Onboarded records that the first-run setup has been completed, so the
	// welcome screen only ever appears once. Absent (false) on a fresh vault.
	Onboarded bool `json:"onboarded,omitempty"`
	// Presence tunes how forward the assistant is about speaking up.
	Presence Presence `json:"presence"`
}

// Presence tunes the ambient, conversational secretary: whether it interjects at
// all, how far ahead it flags a meeting, the minimum quiet gap between non-urgent
// nudges, and hours it stays silent in. Zero values mean "use the defaults" —
// see PresenceDefaults.
type Presence struct {
	Interjections      bool     `json:"interjections"`
	WakeWord           bool     `json:"wake_word"`
	MeetingLeadMinutes int      `json:"meeting_lead_minutes"`
	MinGapMinutes      int      `json:"min_gap_minutes"`
	QuietHours         []string `json:"quiet_hours,omitempty"` // ["22:00", "08:00"]
}

// PresenceDefaults fills unset fields with sensible defaults, so an old config
// (or one that never configured presence) still behaves reasonably. Interjections
// default on and non-urgent nudges are spaced an hour apart — spacing, not a
// per-hour quota, so an imminent meeting is never counted against a tally.
func (p Presence) WithDefaults() Presence {
	if p.MeetingLeadMinutes == 0 {
		p.MeetingLeadMinutes = 10
	}
	if p.MinGapMinutes == 0 {
		p.MinGapMinutes = 60
	}
	return p
}

func path(vault string) string { return filepath.Join(vault, ".brain", "flavor.json") }

func Load(vault string) (*Config, error) {
	cfg := &Config{}

	raw, err := os.ReadFile(path(vault))
	if os.IsNotExist(err) {
		return cfg, nil // a fresh vault: unnamed assistant, default presence
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path(vault), err)
	}
	return cfg, nil
}

func (c *Config) Save(vault string) error {
	if err := vaultpkg.MkdirPrivate(filepath.Dir(path(vault))); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path(vault), raw, 0o600)
}
