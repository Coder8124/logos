package rollup

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Coder8124/brain/internal/provider"
	"github.com/Coder8124/brain/internal/router"
)

// Extraction is deliberately many narrow calls rather than one large prompt.
// Small local models fall apart on multi-objective instructions, and per-task
// calls are individually debuggable — when entity extraction degrades you can
// see it without untangling it from summarisation.
//
// Every call uses sampler-enforced JSON schemas rather than tool definitions.

type Extractor struct {
	rt *router.Router
	p  *provider.Provider
}

func NewExtractor(rt *router.Router) *Extractor {
	return &Extractor{rt: rt, p: rt.Local()}
}

// Category of work in a session.
type Category string

const (
	Work     Category = "work"
	Comms    Category = "comms"
	Research Category = "research"
	Idle     Category = "idle"
)

var classifySchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"category": map[string]any{"type": "string", "enum": []string{"work", "comms", "research", "idle"}},
		"summary":  map[string]any{"type": "string"},
	},
	"required":             []string{"category", "summary"},
	"additionalProperties": false,
}

type Classification struct {
	Category Category `json:"category"`
	Summary  string   `json:"summary"`
}

func (x *Extractor) ClassifySession(s Session) (Classification, string, error) {
	model, err := x.rt.ModelFor(router.T1, true)
	if err != nil {
		return Classification{}, "", err
	}

	const system = "Classify one work session from observed activity. " +
		"Reply with JSON only. The summary must be one short factual sentence " +
		"describing what happened, with no speculation about intent."

	out, err := x.p.Chat(model, system, s.Digest(12), classifySchema)
	if err != nil {
		return Classification{}, model, err
	}

	var c Classification
	if err := json.Unmarshal([]byte(cleanJSON(out)), &c); err != nil {
		return Classification{}, model, fmt.Errorf("classify returned unparseable JSON: %w", err)
	}
	return c, model, nil
}

var entitySchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"entities": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{"type": "string"},
					"type": map[string]any{"type": "string", "enum": []string{"person", "project", "topic", "org"}},
				},
				"required":             []string{"name", "type"},
				"additionalProperties": false,
			},
		},
	},
	"required":             []string{"entities"},
	"additionalProperties": false,
}

type Entity struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

func (x *Extractor) ExtractEntities(s Session) ([]Entity, string, error) {
	model, err := x.rt.ModelFor(router.T1, true)
	if err != nil {
		return nil, "", err
	}

	const system = "Extract named entities that clearly appear in the observed activity. " +
		"Reply with JSON only. Only include an entity if its name literally appears in the input — " +
		"do not infer, expand abbreviations, or add anything you would have to guess. " +
		"Return an empty list rather than guessing. Never include application names, " +
		"website domains, or generic nouns."

	// Deliberately not s.Digest: that includes a list of visited hosts, and
	// showing a small model a list of domains reliably produces a list of
	// domains back. Entities worth a note come from window titles and commit
	// messages, which is what DigestForEntities carries.
	out, err := x.p.Chat(model, system, s.DigestForEntities(12), entitySchema)
	if err != nil {
		return nil, model, err
	}

	var res struct {
		Entities []Entity `json:"entities"`
	}
	if err := json.Unmarshal([]byte(cleanJSON(out)), &res); err != nil {
		return nil, model, fmt.Errorf("entity extraction returned unparseable JSON: %w", err)
	}

	// The model is told not to return app names; enforce it rather than trust
	// it. This is the single noisiest failure mode in practice.
	var kept []Entity
	for _, e := range res.Entities {
		if isNoise(e.Name) {
			continue
		}
		kept = append(kept, e)
	}
	return kept, model, nil
}

// WriteDaily produces the day's prose from the session table.
//
// Built from session digests, never from previously written summaries. Weekly
// notes built from daily notes drift into mush within a month, so every rollup
// re-derives from the episodic source.
func (x *Extractor) WriteDaily(date string, sessions []Session, classes []Classification) (string, string, error) {
	model, err := x.rt.ModelFor(router.T2, false)
	if err != nil {
		return "", "", err
	}

	var b strings.Builder
	for i, s := range sessions {
		fmt.Fprintf(&b, "%s\n", s.Digest(8))
		if i < len(classes) {
			fmt.Fprintf(&b, "Classified: %s — %s\n", classes[i].Category, classes[i].Summary)
		}
		b.WriteString("\n")
	}

	system := "Write a short factual daily log in markdown from observed computer activity. " +
		"Use past tense and plain prose. Three to six bullet points. " +
		"State only what the observations support; do not speculate about motives, " +
		"mood, or productivity. Do not invent names. No preamble, no headings."

	out, err := x.p.Chat(model, system,
		fmt.Sprintf("Date: %s\n\n%s", date, b.String()), nil)
	if err != nil {
		return "", model, err
	}
	return strings.TrimSpace(out), model, nil
}

// cleanJSON strips the markdown fences some models wrap JSON in despite being
// asked for raw JSON. Cheap insurance; the schema does the real work.
func cleanJSON(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		}
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	}
	return strings.TrimSpace(s)
}

// Names that are never worth a note. Extracted entities that are really just
// the tools used to do the work.
var noiseNames = map[string]bool{
	"chrome": true, "google chrome": true, "arc": true, "safari": true, "firefox": true,
	"slack": true, "discord": true, "ghostty": true, "terminal": true, "iterm": true,
	"vscode": true, "visual studio code": true, "intellij": true, "obsidian": true,
	"finder": true, "github": true, "gitlab": true, "google": true, "youtube": true,
	"localhost": true, "unknown": true, "n/a": true, "none": true,
}

func isNoise(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" || len(n) < 2 || noiseNames[n] {
		return true
	}
	// Domains are the dominant false positive: a small model shown a list of
	// hosts will extract them as entities no matter how firmly the prompt says
	// not to. Rejecting them structurally is the only reliable fix.
	if looksLikeDomain(n) {
		return true
	}
	return false
}

var tlds = []string{"com", "org", "net", "io", "gg", "app", "dev", "co", "ai", "gov", "edu"}

func looksLikeDomain(n string) bool {
	if strings.HasPrefix(n, "http") || strings.Contains(n, "/") {
		return true
	}
	i := strings.LastIndex(n, ".")
	if i < 0 || i == len(n)-1 {
		return false
	}
	suffix := n[i+1:]
	for _, tld := range tlds {
		if suffix == tld {
			return true
		}
	}
	return false
}
