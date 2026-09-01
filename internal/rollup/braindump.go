package rollup

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Coder8124/brain/internal/router"
)

// Braindump: a scrap of text in, a classified, routed proposal out.
//
// Inspired by COG-second-brain's braindump-with-auto-classification (MIT, see
// CREDITS.md). The point is capture friction: a thought you have to file
// correctly is a thought you don't write down. You dump it raw; the system
// decides whether it is a task, a note about someone, or a topic, and proposes
// where it goes — you approve.

var dumpSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"kind":  map[string]any{"type": "string", "enum": []string{"task", "person", "project", "topic", "idea"}},
		"title": map[string]any{"type": "string"},
		"body":  map[string]any{"type": "string"},
	},
	"required":             []string{"kind", "title", "body"},
	"additionalProperties": false,
}

// Braindump classifies raw text and queues a proposal for it. Returns the
// proposal so the caller can report what it decided.
//
// Tasks are handled by the caller (they become open loops, not notes); this
// returns the classification for everything, and a note proposal for the rest.
func Braindump(db *sql.DB, rt *router.Router, text string) (*Proposal, string, error) {
	model, err := rt.ModelFor(router.T1, true)
	if err != nil {
		return nil, "", err
	}

	const system = "Classify a quick captured thought and file it. Reply with JSON only. " +
		"kind is task (something to do), person, project, topic, or idea. " +
		"title is a few words. body restates the thought as a clean sentence or two, " +
		"preserving the meaning without inventing detail."

	out, err := rt.Local().Chat(model, system, text, dumpSchema)
	if err != nil {
		return nil, "", err
	}
	var res struct {
		Kind, Title, Body string
	}
	if err := json.Unmarshal([]byte(cleanJSON(out)), &res); err != nil {
		return nil, "", fmt.Errorf("braindump classification failed to parse: %w", err)
	}
	res.Title = strings.TrimSpace(res.Title)
	res.Body = strings.TrimSpace(res.Body)
	if res.Body == "" {
		res.Body = strings.TrimSpace(text) // never lose the original thought
	}

	// A task is an open loop, not a note — the caller routes it to the secretary.
	if res.Kind == "task" {
		return nil, "task", nil
	}

	folder := map[string]string{
		"person": "people", "project": "projects", "topic": "topics", "idea": "ideas",
	}[res.Kind]
	if folder == "" {
		folder = "notes"
	}

	// Braindumps have no episodic events behind them; the raw text is the
	// evidence. We record it as a synthetic evidence marker so the proposal
	// still satisfies the "nothing without evidence" invariant, while being
	// honest that the source is a manual capture rather than an observation.
	prop := &Proposal{
		Kind:     NewNote,
		Target:   folder + "/" + slugify(res.Title),
		Conf:     0.6,
		Evidence: []int64{-1}, // -1 marks "manual braindump", no event row
		Model:    model,
		Payload:  Payload{Title: res.Title, Type: res.Kind, Body: res.Body},
	}
	// If a note by this name exists, fold in as an append instead of colliding.
	var exists int
	db.QueryRow("SELECT COUNT(*) FROM notes WHERE slug = ?", prop.Target).Scan(&exists)
	if exists > 0 {
		prop.Kind = Append
		prop.Payload = Payload{Body: res.Body}
	}

	if err := Enqueue(db, prop); err != nil {
		return nil, res.Kind, err
	}
	return prop, res.Kind, nil
}
