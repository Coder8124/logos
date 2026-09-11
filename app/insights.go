package main

import (
	"github.com/Coder8124/brain/internal/insight"
	"github.com/Coder8124/brain/internal/memory"
	"github.com/Coder8124/brain/internal/session"
)

// The insights view is a second lens on the tree pane's vault, not a second
// store — internal/insight holds no state (see its package doc), so this
// binding does no more than the CLI's runInsights: init the tables the
// generators read, call Generate, and hand the result to the frontend.

// Insight mirrors internal/insight.Insight for the frontend — a plain struct
// rather than the package type itself, so the JSON field names are stable
// and independent of internal/insight's own field tags (it has none).
type Insight struct {
	Kind    string   `json:"kind"`
	Text    string   `json:"text"`
	Sources []string `json:"sources"`
}

// Drop mirrors internal/insight.Drop, so the panel can show why a candidate
// did not make it into the list instead of the list just quietly being one
// shorter than the vault actually supports (invariant 4).
type Drop struct {
	Claim  string `json:"claim"`
	Reason string `json:"reason"`
}

// InsightsView bundles the findings with the mechanical-tier notice — the
// panel needs the notice on every load, not just once, because a stale cached
// screen with no notice reads as a promise of a fuller tier that does not exist.
type InsightsView struct {
	Insights []Insight `json:"insights"`
	Drops    []Drop    `json:"drops"`
	Degraded string    `json:"degraded"`
}

// Insights runs the mechanical generators against the vault, scoped to
// project ("" for every project). Read-only — it writes neither the vault nor
// the index, matching internal/insight's derived-on-demand design.
func (a *App) Insights(project string) (InsightsView, error) {
	ix, err := a.open()
	if err != nil {
		return InsightsView{}, err
	}
	defer ix.Close()
	if err := memory.Init(ix.DB); err != nil {
		return InsightsView{}, err
	}
	if err := session.Init(ix.DB); err != nil {
		return InsightsView{}, err
	}

	found, dropped, err := insight.Generate(ix.DB, a.vault, project)
	if err != nil {
		return InsightsView{}, err
	}

	view := InsightsView{Degraded: insight.Degraded}
	for _, in := range found {
		view.Insights = append(view.Insights, Insight{Kind: in.Kind, Text: in.Text, Sources: in.Sources})
	}
	for _, d := range dropped {
		view.Drops = append(view.Drops, Drop{Claim: d.Claim, Reason: d.Reason})
	}
	return view, nil
}
