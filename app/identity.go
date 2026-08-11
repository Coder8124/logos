package main

import (
	"strings"

	"github.com/pragun/brain/internal/flavor"
)

// Identity is who the two parties in the conversation are: what the assistant
// calls the user, and what the user calls the assistant. The first-run setup
// screen collects both; the header's settings affordance edits them later. One
// binding backs both, so the welcome screen and the settings sheet cannot drift.

// IdentityView is what the setup screen and settings sheet render.
type IdentityView struct {
	UserName  string `json:"userName"`  // what the assistant calls the user
	AgentName string `json:"agentName"` // what the user calls the assistant
	Onboarded bool   `json:"onboarded"` // first-run setup has been completed
}

// Identity returns the current names and use case. The frontend calls this on
// boot: if Onboarded is false it shows the welcome screen, otherwise it wires
// the settings sheet to these values.
func (a *App) Identity() (IdentityView, error) {
	cfg, err := flavor.Load(a.vault)
	if err != nil {
		return IdentityView{}, err
	}
	return IdentityView{
		UserName:  cfg.UserName,
		AgentName: cfg.Name,
		Onboarded: cfg.Onboarded,
	}, nil
}

// SaveIdentity persists the setup answers and marks onboarding complete. It is
// the single writer behind both the welcome screen and later edits, so a blank
// name simply leaves the assistant unnamed rather than erroring.
func (a *App) SaveIdentity(userName, agentName string) error {
	cfg, err := flavor.Load(a.vault)
	if err != nil {
		return err
	}
	cfg.UserName = strings.TrimSpace(userName)
	cfg.Name = strings.TrimSpace(agentName)
	cfg.Onboarded = true
	return cfg.Save(a.vault)
}
