package main

import (
	"strings"
	"time"

	"github.com/pragun/brain/internal/dream"
	"github.com/pragun/brain/internal/flavor"
	"github.com/pragun/brain/internal/presence"
	"github.com/pragun/brain/internal/secretary"
)

// The presence, in the panel. The widget is a window onto the ambient secretary,
// not a second mouth: it peeks at what could be raised (via Check) rather than
// consuming the spoken cadence, so opening the panel never burns a nudge the voice
// loop should deliver.

// NudgeView is one thing the presence would raise, shaped for the panel.
type NudgeView struct {
	Kind     string `json:"kind"`
	Text     string `json:"text"`
	Detail   string `json:"detail"`
	Critical bool   `json:"critical"`
}

// PresenceView is what the panel shows for the ambient secretary: how it's
// addressed, its opening line, and the single most pressing nudge, if any.
type PresenceView struct {
	Name     string     `json:"name"`
	Greeting string     `json:"greeting"`
	Nudge    *NudgeView `json:"nudge,omitempty"`
}

// Presence returns the greeting and the top interjection for the panel.
func (a *App) Presence() (PresenceView, error) {
	ix, err := a.open()
	if err != nil {
		return PresenceView{}, err
	}
	defer ix.Close()
	presence.Init(ix.DB)
	dream.InitQueue(ix.DB)

	cfg, err := flavor.Load(a.vault)
	if err != nil {
		return PresenceView{}, err
	}
	prefs := cfg.Presence.WithDefaults()

	now := time.Now()
	b, err := secretary.Compose(ix.DB, now)
	if err != nil {
		return PresenceView{}, err
	}
	view := PresenceView{Name: cfg.Name, Greeting: b.Greeting}

	if items, err := presence.Check(ix.DB, now, prefs.MeetingLeadMinutes); err == nil && len(items) > 0 {
		it := items[0]
		view.Nudge = &NudgeView{Kind: string(it.Kind), Text: it.Text, Detail: it.Detail, Critical: it.Critical}
	}
	return view, nil
}

// Name returns the assistant's name (empty if unset).
func (a *App) Name() (string, error) {
	cfg, err := flavor.Load(a.vault)
	if err != nil {
		return "", err
	}
	return cfg.Name, nil
}

// SetName gives the assistant a name — how you address it and, with a wake-word
// model, the word that wakes it.
func (a *App) SetName(name string) error {
	cfg, err := flavor.Load(a.vault)
	if err != nil {
		return err
	}
	cfg.Name = strings.TrimSpace(name)
	return cfg.Save(a.vault)
}
