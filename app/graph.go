package main

import (
	"fmt"
	"net/url"
	"path/filepath"
	"time"

	"github.com/Coder8124/brain/internal/graph"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// Graph bindings back the memory-graph view. Extraction is pure Go over the
// index; the frontend does the force layout and rendering, since ego-mode keeps
// the node count small enough that a canvas simulation is smooth.

// GraphView returns the ego-graph around a focus node. An empty focus resolves
// to today's daily note, or the vault's hub when there is no note for today.
func (a *App) GraphView(focus string, hops int, similarity bool) (graph.Graph, error) {
	ix, err := a.open()
	if err != nil {
		return graph.Graph{}, err
	}
	defer ix.Close()

	if focus == "" {
		focus = graph.DefaultFocus(ix.DB, "daily/"+time.Now().Format("2006-01-02"))
	}
	return graph.Ego(ix.DB, focus, hops, similarity)
}

// GraphFind resolves a search query to a node slug, for the "/" jump box.
func (a *App) GraphFind(query string) string {
	ix, err := a.open()
	if err != nil {
		return ""
	}
	defer ix.Close()
	slug, _ := graph.Find(ix.DB, query)
	return slug
}

// OpenInObsidian opens a note in Obsidian via its URL scheme. The vault name is
// the vault directory's base name, which is how Obsidian identifies an open vault.
func (a *App) OpenInObsidian(slug string) error {
	vaultName := filepath.Base(a.vault)
	u := fmt.Sprintf("obsidian://open?vault=%s&file=%s",
		url.QueryEscape(vaultName), url.QueryEscape(slug))
	runtime.BrowserOpenURL(a.ctx, u)
	return nil
}
