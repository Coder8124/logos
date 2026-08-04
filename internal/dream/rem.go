package dream

import (
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/pragun/brain/internal/memory"
	"github.com/pragun/brain/internal/provider"
	"github.com/pragun/brain/internal/router"
)

// MaxInsights caps how many connections a single night may propose. A review
// queue flooded with speculation is a queue the user stops reading, and an
// unread queue is the same as no confirmation gate at all.
const MaxInsights = 3

// remCandidates is how many salient memories the pass considers when hunting for
// distant pairs to bridge.
const remCandidates = 20

// remConfidence is the seed confidence for a dreamed connection: a hypothesis
// that must be corroborated before it firms up.
const remConfidence = 0.5

var connectionSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"connected": map[string]any{"type": "boolean"},
		"insight":   map[string]any{"type": "string"},
	},
	"required":             []string{"connected"},
	"additionalProperties": false,
}

// rem recombines the cleaned store into candidate connections. It runs over the
// most salient memories, tests *distant* pairs (across different work, where the
// interesting links live), and enqueues what the model judges a real connection —
// capped, grounded in two real memories, and seeded at a low confidence.
func rem(db *sql.DB, rt *router.Router, dryRun bool, res *Result) error {
	model, err := rt.ModelFor(router.T2, true)
	if err != nil {
		// No reasoning model resolved. A missed night of dreaming is worth more
		// than a fabricated one — skip REM rather than run it shallow on T1.
		res.REMSkipped = true
		return nil
	}

	mems, err := memory.Surface(db, nil, remCandidates)
	if err != nil {
		return err
	}
	if len(mems) < 2 {
		return nil
	}

	p := rt.Local()
	for _, pair := range distantPairs(mems) {
		if res.Insights >= MaxInsights {
			break
		}
		a, b := pair[0], pair[1]
		text, ok := judgeConnection(p, model, a, b)
		if !ok {
			continue
		}
		in := &Insight{
			Kind:      Connection,
			Text:      text,
			EndpointA: a.ID,
			EndpointB: b.ID,
			Conf:      remConfidence,
			Model:     model,
		}
		// The grounding guard is structural: selection guarantees both endpoints,
		// and Enqueue's Validate refuses anything that lost one. Under dry run we
		// only count what would clear that bar.
		if dryRun {
			if in.Validate() == nil {
				res.Insights++
			}
			continue
		}
		if err := Enqueue(db, in); err == nil {
			res.Insights++
		}
	}
	return nil
}

// distantPairs proposes memory pairs worth testing for a hidden connection:
// ones drawn from *different* projects (or, absent projects, different kinds),
// since the links worth surfacing are across work, not within it. The number of
// pairs tried is bounded so the per-night model budget stays small.
func distantPairs(mems []memory.Memory) [][2]memory.Memory {
	const maxTried = MaxInsights * 4
	var out [][2]memory.Memory
	for i := 0; i < len(mems) && len(out) < maxTried; i++ {
		for j := i + 1; j < len(mems) && len(out) < maxTried; j++ {
			if distant(mems[i], mems[j]) {
				out = append(out, [2]memory.Memory{mems[i], mems[j]})
			}
		}
	}
	return out
}

func distant(a, b memory.Memory) bool {
	if a.Project != "" && b.Project != "" {
		return a.Project != b.Project
	}
	return a.Kind != b.Kind
}

// judgeConnection asks the reasoning model whether two memories genuinely connect,
// and if so to phrase it in one sentence. It is told to invent nothing beyond the
// two facts given — the model narrates a link, it does not manufacture one.
func judgeConnection(p *provider.Provider, model string, a, b memory.Memory) (string, bool) {
	const system = "You are the offline, dreaming part of a personal assistant, recombining what it " +
		"knows while the user is away. Given two things it has learned, decide whether there is a " +
		"genuine, non-obvious connection worth surfacing — a shared theme, a tension between them, or " +
		"an idea one suggests about the other. Invent nothing beyond the two facts given. If there is " +
		"no real connection, set connected to false. When true, put one plain sentence in insight. JSON only."

	out, err := p.Chat(model, system, "A: "+a.Text+"\nB: "+b.Text, connectionSchema)
	if err != nil {
		return "", false
	}
	var r struct {
		Connected bool   `json:"connected"`
		Insight   string `json:"insight"`
	}
	if json.Unmarshal([]byte(cleanJSON(out)), &r) != nil {
		return "", false
	}
	if !r.Connected || strings.TrimSpace(r.Insight) == "" {
		return "", false
	}
	return strings.TrimSpace(r.Insight), true
}

// cleanJSON trims prose a small local model sometimes wraps around its JSON.
func cleanJSON(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '{'); i > 0 {
		s = s[i:]
	}
	if i := strings.LastIndexByte(s, '}'); i >= 0 {
		s = s[:i+1]
	}
	return s
}
