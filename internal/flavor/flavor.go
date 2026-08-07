// Package flavor is the personality the assistant wears.
//
// The same engine — capture, vault, brief — serves every flavor; a flavor only
// changes what gets emphasised and which extra capabilities are switched on.
// Secretary is the base: proactive about your day. Tutor turns the vault into
// study material and takes notes off your screen. Business reaches out through
// MCP servers to summarise data.
package flavor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Flavor string

const (
	Secretary Flavor = "secretary"
	Tutor     Flavor = "tutor"
	Business  Flavor = "business"
)

// All returns the flavors this edition offers (see edition.go).
func All() []Flavor { return Offered }

func offered(f Flavor) bool {
	for _, o := range Offered {
		if o == f {
			return true
		}
	}
	return false
}

// Parse accepts only a flavor this edition actually offers, so `brain mode
// business` fails cleanly on the student build rather than half-working.
func Parse(s string) (Flavor, error) {
	f := Flavor(strings.ToLower(strings.TrimSpace(s)))
	switch f {
	case Secretary, Tutor, Business:
		if !offered(f) {
			return "", fmt.Errorf("this edition (%s) does not offer the %q flavor", EditionName, f)
		}
		return f, nil
	}
	return "", fmt.Errorf("unknown flavor %q", s)
}

func (f Flavor) Describe() string {
	switch f {
	case Tutor:
		return "turns the vault into study material and takes notes off your screen"
	case Business:
		return "reaches through MCP servers to summarise data and trends"
	default:
		return "proactive about your day: meetings, open loops, routines"
	}
}

// Config is the flavor layer's slice of .brain/config.json. Kept in its own file
// rather than folded into the model router config, so switching personas never
// risks disturbing model or key settings.
type Config struct {
	Active Flavor `json:"active"`
	// ScreenNotes gates the tutor screen-capture pipeline. Off by default and
	// only ever consulted in tutor mode — screen capture is the most invasive
	// thing the system can do, so it takes a deliberate opt-in.
	ScreenNotes bool `json:"screen_notes"`
	// MCP servers business mode may reach. Empty until the user adds one.
	MCP []MCPServer `json:"mcp,omitempty"`
	// Name is what the user calls the assistant — how they address it and, when a
	// wake-word model is present, the word that wakes it. Empty means unnamed.
	Name string `json:"name,omitempty"`
	// UserName is what the assistant calls the user — used to warm the greeting
	// ("Morning, Pragun"). Empty means it stays impersonal.
	UserName string `json:"user_name,omitempty"`
	// Onboarded records that the first-run setup has been completed, so the
	// welcome screen only ever appears once. Absent (false) on a fresh vault.
	Onboarded bool `json:"onboarded,omitempty"`
	// Presence tunes the ambient secretary.
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

// MCPServer is a stdio MCP server brain can launch and talk to.
type MCPServer struct {
	Name    string            `json:"name"`
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

func path(vault string) string { return filepath.Join(vault, ".brain", "flavor.json") }

func Load(vault string) (*Config, error) {
	cfg := &Config{Active: Default}

	raw, err := os.ReadFile(path(vault))
	if os.IsNotExist(err) {
		return cfg, nil // absent config means this edition's default flavor
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path(vault), err)
	}
	// A saved flavor this edition no longer offers (e.g. a config carried over
	// from the full build) falls back to the default rather than showing a
	// persona the product does not have.
	if cfg.Active == "" || !offered(cfg.Active) {
		cfg.Active = Default
	}
	return cfg, nil
}

func (c *Config) Save(vault string) error {
	if err := os.MkdirAll(filepath.Dir(path(vault)), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path(vault), raw, 0o600)
}
