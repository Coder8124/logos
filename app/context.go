package main

import (
	"github.com/Coder8124/brain/internal/contextpack"
	"github.com/Coder8124/brain/internal/router"
	"github.com/Coder8124/brain/internal/session"
)

// The context view answers the question the whole product is built around:
// if an agent arrived right now and asked for this, what would it actually get?
// contextpack.Build/Render is the real assembly path — the same one MCP's
// context_pack tool calls — so this binding does not reimplement it, only
// exposes it.

// ContextPreviewView is what an arriving agent would receive for a task,
// rendered exactly as contextpack would hand it over, plus the assembled Pack
// so the panel can show a structured breakdown alongside the raw text.
type ContextPreviewView struct {
	Pack     contextpack.Pack `json:"pack"`
	Markdown string           `json:"markdown"`
}

// ContextPreview builds and renders a context pack for a task, optionally
// narrowed by a project hint. Task may be empty — an empty task still resolves
// standing context (checkpoint, project, open loops) for the hinted project.
func (a *App) ContextPreview(hint, task string) (ContextPreviewView, error) {
	ix, err := a.open()
	if err != nil {
		return ContextPreviewView{}, err
	}
	defer ix.Close()

	rt, err := a.router()
	if err != nil {
		return ContextPreviewView{}, err
	}
	embedModel, _ := rt.Model(router.T0)

	pack, err := contextpack.Build(ix, rt.Local(), embedModel, contextpack.Request{
		Task: task,
		Hint: hint,
	})
	if err != nil {
		return ContextPreviewView{}, err
	}
	md := pack.Render() // fills Budget, Sources, Excluded as a side effect
	return ContextPreviewView{Pack: pack, Markdown: md}, nil
}

// Projects lists the project scopes that have at least one checkpoint, for the
// context view's project picker. Read from the vault, like Checkpoints — a
// project only shows up once work has actually been recorded under it.
func (a *App) Projects() ([]string, error) {
	return session.Projects(a.vault)
}
