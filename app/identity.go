package main

import (
	"strings"

	"github.com/pragun/brain/internal/flavor"
)

// Identity is who the two parties in the conversation are: what the assistant
// calls the user, what the user calls the assistant, and which persona
// (use-case) it leads with. The first-run setup screen collects all three; the
// header's settings affordance edits them later. One binding backs both, so the
// welcome screen and the settings sheet can never drift.

// IdentityView is what the setup screen and settings sheet render.
type IdentityView struct {
	UserName  string `json:"userName"`  // what the assistant calls the user
	AgentName string `json:"agentName"` // what the user calls the assistant
	Usecase   string `json:"usecase"`   // the active flavor, i.e. primary use case
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
		Usecase:   string(cfg.Active),
		Onboarded: cfg.Onboarded,
	}, nil
}

// SaveIdentity persists the three setup answers and marks onboarding complete.
// It is the single writer behind both the welcome screen and later edits, so a
// blank agent name simply leaves the assistant unnamed rather than erroring —
// the only hard requirement is a use case the edition actually offers.
func (a *App) SaveIdentity(userName, agentName, usecase string) error {
	cfg, err := flavor.Load(a.vault)
	if err != nil {
		return err
	}
	cfg.UserName = strings.TrimSpace(userName)
	cfg.Name = strings.TrimSpace(agentName)
	if usecase = strings.TrimSpace(usecase); usecase != "" {
		f, err := flavor.Parse(usecase)
		if err != nil {
			return err
		}
		cfg.Active = f
	}
	cfg.Onboarded = true
	return cfg.Save(a.vault)
}
