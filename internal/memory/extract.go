package memory

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pragun/brain/internal/router"
)

// Learning from conversations. After an exchange, the model is asked what — if
// anything — is worth remembering about the user for next time. The bar is
// deliberately high: durable facts, preferences, and standing context, never
// the passing content of the chat. A memory of "the user asked about revenue
// once" is noise; "the user prefers figures rounded to the nearest thousand" is
// signal.

var extractSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"memories": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"text":     map[string]any{"type": "string"},
					"kind":     map[string]any{"type": "string", "enum": []string{"preference", "person", "fact", "context"}},
					"salience": map[string]any{"type": "number"},
				},
				"required":             []string{"text", "kind", "salience"},
				"additionalProperties": false,
			},
		},
	},
	"required":             []string{"memories"},
	"additionalProperties": false,
}

// Learn extracts durable memories from a conversation excerpt and stores the
// ones worth keeping. Returns how many were newly stored. Runs at T1 — a narrow
// classification, not reasoning.
func Learn(db *sql.DB, rt *router.Router, exchange, source string) (int, error) {
	model, err := rt.ModelFor(router.T1, true)
	if err != nil {
		return 0, err
	}

	const system = "You maintain a personal assistant's long-term memory of its user. From the " +
		"conversation, extract ONLY things worth remembering for future sessions: the user's " +
		"stable preferences, facts about people they work with, and standing context or priorities. " +
		"Reply with JSON only. Do NOT record the passing topic of this chat, questions asked, or " +
		"anything that will not matter next week. Salience 0-1 reflects how important the memory is. " +
		"Return an empty list if nothing durable was said."

	out, err := rt.Local().Chat(model, system, truncate(exchange, 4000), extractSchema)
	if err != nil {
		return 0, err
	}
	var res struct {
		Memories []struct {
			Text     string  `json:"text"`
			Kind     string  `json:"kind"`
			Salience float64 `json:"salience"`
		} `json:"memories"`
	}
	if err := json.Unmarshal([]byte(cleanJSON(out)), &res); err != nil {
		return 0, fmt.Errorf("memory extraction did not parse: %w", err)
	}

	embed, _ := rt.Model(router.T0)
	stored := 0
	for _, m := range res.Memories {
		if strings.TrimSpace(m.Text) == "" {
			continue
		}
		sal := m.Salience
		if sal <= 0 || sal > 1 {
			sal = 0.5
		}
		r, err := Store(db, rt.Local(), embed, &Memory{
			Text: strings.TrimSpace(m.Text), Kind: Kind(m.Kind), Salience: sal, Source: source,
		})
		if err != nil {
			return stored, err
		}
		if r.Created() {
			stored++
		}
	}
	return stored, nil
}

// Context renders the recalled memories as a compact block for injection into a
// prompt — what the assistant "already knows" about the user, so it does not ask
// again and can act on preferences unprompted.
func Render(mems []Memory) string {
	if len(mems) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("What you remember about the user:\n")
	for _, m := range mems {
		fmt.Fprintf(&b, "- (%s) %s\n", m.Kind, m.Text)
	}
	return b.String()
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

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
