package memory

import (
	"database/sql"
	"encoding/json"
	"math"
	"strings"
	"time"

	"github.com/Coder8124/brain/internal/router"
)

// Making memory smarter: it should behave less like an append-only log and more
// like a memory — what is used stays sharp, what is not fades, near-duplicates
// merge, and a fact that gets updated supersedes the old one so the current
// truth is what surfaces. These are the maintenance operations that keep the
// store honest as it grows.

// HalfLifeDays is how long an untouched memory takes to lose half its salience.
// Long, because a preference stated once is meant to persist — but not forever
// if it is never relevant again.
const HalfLifeDays = 90.0

// EffectiveSalience is a memory's stored salience decayed by how long since it
// was last useful, then lifted by how often it has proven useful. This is what
// ranking should trust, not the raw stored number: memory that earns its keep
// through recall stays prominent; memory that never helps recedes.
func EffectiveSalience(m Memory, now int64) float64 {
	ref := m.LastUsed
	if ref == 0 {
		ref = m.Created
	}
	ageDays := float64(now-ref) / 86400
	if ageDays < 0 {
		ageDays = 0
	}
	decay := math.Pow(0.5, ageDays/HalfLifeDays)
	// Reinforcement: a memory recalled many times resists decay.
	boost := 1 + math.Min(0.5, float64(m.Uses)*0.05)
	return math.Min(1, m.Salience*decay*boost)
}

// Decay lowers the stored salience of memories that have gone unused, so the
// store's own sense of importance drifts toward what is actually exercised.
// Idempotent enough to run periodically; the floor stops anything vanishing.
func Decay(db *sql.DB, now int64) (int, error) {
	rows, err := db.Query(`SELECT id, salience, created, last_used, uses FROM memories WHERE superseded = 0`)
	if err != nil {
		return 0, err
	}
	type upd struct {
		id  int64
		sal float64
	}
	var updates []upd
	for rows.Next() {
		var id int64
		var m Memory
		if err := rows.Scan(&id, &m.Salience, &m.Created, &m.LastUsed, &m.Uses); err != nil {
			rows.Close()
			return 0, err
		}
		eff := EffectiveSalience(m, now)
		if eff < m.Salience-0.01 {
			updates = append(updates, upd{id, math.Max(0.05, eff)})
		}
	}
	rows.Close()
	for _, u := range updates {
		db.Exec("UPDATE memories SET salience = ? WHERE id = ?", u.sal, u.id)
	}
	return len(updates), nil
}

// Surface returns the top non-superseded memories of the given kinds by
// effective salience — what the assistant should proactively keep in mind (the
// standing preferences and context that make a brief feel like it knows you),
// with no query needed.
func Surface(db *sql.DB, kinds []Kind, n int) ([]Memory, error) {
	mems, err := activeMemories(db)
	if err != nil {
		return nil, err
	}
	want := map[Kind]bool{}
	for _, k := range kinds {
		want[k] = true
	}
	now := time.Now().Unix()
	var filtered []Memory
	for _, m := range mems {
		if len(want) == 0 || want[m.Kind] {
			m.Score = EffectiveSalience(m, now)
			filtered = append(filtered, m)
		}
	}
	sortByScore(filtered)
	if len(filtered) > n {
		filtered = filtered[:n]
	}
	return filtered, nil
}

// activeMemories loads non-superseded memories with their vectors, for the
// consolidation pass.
func activeMemories(db *sql.DB) ([]Memory, error) {
	rows, err := db.Query(`SELECT id, text, kind, salience, source, created, last_used, uses, vec FROM memories WHERE superseded = 0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Memory
	for rows.Next() {
		var m Memory
		var kind string
		if err := rows.Scan(&m.ID, &m.Text, &kind, &m.Salience, &m.Source, &m.Created, &m.LastUsed, &m.Uses, &m.vec); err != nil {
			return nil, err
		}
		m.Kind = Kind(kind)
		out = append(out, m)
	}
	return out, rows.Err()
}

var updateSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"relation": map[string]any{"type": "string", "enum": []string{"duplicate", "update", "distinct"}},
	},
	"required":             []string{"relation"},
	"additionalProperties": false,
}

// Consolidate merges near-duplicate memories and supersedes ones a newer memory
// updates, so the store holds each fact once and holds its current version.
//
// For each highly-similar pair, the model classifies the relation:
//   - duplicate → keep the more-salient, fold in salience, supersede the other
//   - update    → the newer memory replaces the older; the older is superseded
//     (this is what the knowledge-update case needs: "I moved to Boston"
//     supersedes "I live in NYC")
//   - distinct  → leave both
//
// Only pairs above the similarity gate are shown to the model, so the number of
// calls stays proportional to genuine overlap, not to the store size squared.
func Consolidate(db *sql.DB, rt *router.Router) (merged int, superseded int, err error) {
	mems, err := activeMemories(db)
	if err != nil {
		return 0, 0, err
	}
	model, err := rt.ModelFor(router.T1, true)
	if err != nil {
		return 0, 0, err
	}

	gone := map[int64]bool{}
	const gate = 0.80

	for i := 0; i < len(mems); i++ {
		if gone[mems[i].ID] {
			continue
		}
		for j := i + 1; j < len(mems); j++ {
			if gone[mems[j].ID] {
				continue
			}
			if len(mems[i].vec) == 0 || len(mems[j].vec) == 0 {
				continue
			}
			if cosine(blobToFloats(mems[i].vec), blobToFloats(mems[j].vec)) < gate {
				continue
			}

			// i is older (lower id / earlier), j is newer.
			older, newer := mems[i], mems[j]
			if older.Created > newer.Created {
				older, newer = newer, older
			}

			rel := classify(rt, model, older.Text, newer.Text)
			switch rel {
			case "duplicate":
				// Keep the more salient; fold salience in, and lift confidence —
				// two independent statements of a fact are stronger than one.
				keep, drop := newer, older
				if older.Salience >= newer.Salience {
					keep, drop = older, newer
				}
				db.Exec("UPDATE memories SET salience = ?, confidence = MIN(1.0, confidence + 0.05), uses = uses + ? WHERE id = ?",
					math.Min(1, keep.Salience+0.1), drop.Uses, keep.ID)
				db.Exec("UPDATE memories SET superseded = 1, superseded_by = ? WHERE id = ?", keep.ID, drop.ID)
				logEvent(db, keep.ID, EvMerged, keep.Text, drop.ID)
				logEvent(db, drop.ID, EvSuperseded, drop.Text, keep.ID)
				gone[drop.ID] = true
				merged++
			case "update":
				// The newer fact wins; the older is superseded but retained, with a
				// pointer to what replaced it so the timeline can show the change.
				db.Exec("UPDATE memories SET superseded = 1, superseded_by = ? WHERE id = ?", newer.ID, older.ID)
				logEvent(db, older.ID, EvSuperseded, older.Text, newer.ID)
				gone[older.ID] = true
				superseded++
			}
		}
	}
	// Consolidation retires memories wholesale, so the files have to follow or
	// the next import would restore everything it just superseded.
	if merged+superseded > 0 {
		for _, k := range kinds {
			if err := flush(db, k); err != nil {
				return merged, superseded, err
			}
		}
	}
	return merged, superseded, nil
}

func classify(rt *router.Router, model, older, newer string) string {
	const system = "Two memories about a user are highly similar. Decide their relation. " +
		"'duplicate' = they say the same thing. 'update' = the newer one changes or replaces the " +
		"older (a moved city, a changed job, a new preference that overrides the old). " +
		"'distinct' = they are genuinely different facts that happen to be related. Reply with JSON only."
	out, err := rt.Local().Chat(model, system, "OLDER: "+older+"\nNEWER: "+newer, updateSchema)
	if err != nil {
		return "distinct"
	}
	var res struct {
		Relation string `json:"relation"`
	}
	if json.Unmarshal([]byte(cleanJSON(strings.TrimSpace(out))), &res) != nil {
		return "distinct"
	}
	return res.Relation
}
