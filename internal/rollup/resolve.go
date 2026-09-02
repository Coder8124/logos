package rollup

import (
	"database/sql"
	"strings"

	"github.com/Coder8124/brain/internal/vault"
)

// Entity resolution: "Sam", "Sameer" and "@sam" must converge on one note.
//
// This runs before a note is ever created. Duplicate-entity explosion is the
// failure mode that makes an auto-written vault worthless, and it is very hard
// to unwind after a few weeks of accumulation.

type Match struct {
	Slug string
	// Certain is true for an exact slug or alias hit. Uncertain matches become
	// merge proposals for the user to settle rather than silent decisions.
	Certain bool
}

// Resolve finds the existing note an entity refers to, if any.
//
// Deliberately only does the two cheap, safe steps: exact slug and declared
// alias. Embedding-similarity matching is available but is routed through a
// merge proposal instead of applied here — a wrong automatic merge silently
// destroys information, while a wrong proposal costs one keystroke.
func Resolve(db *sql.DB, name, kind string) (Match, bool) {
	norm := vault.NormalizeLink(name)
	if norm == "" {
		return Match{}, false
	}

	// Exact slug, preferring the note whose type matches.
	rows, err := db.Query(
		`SELECT slug, kind FROM notes WHERE slug = ? OR slug LIKE '%/' || ?`, norm, norm)
	if err == nil {
		defer rows.Close()
		var fallback string
		for rows.Next() {
			var slug, k string
			if rows.Scan(&slug, &k) != nil {
				continue
			}
			if k == kind {
				return Match{Slug: slug, Certain: true}, true
			}
			if fallback == "" {
				fallback = slug
			}
		}
		if fallback != "" {
			return Match{Slug: fallback, Certain: true}, true
		}
	}

	// Declared alias — the durable result of a past user decision.
	var slug string
	err = db.QueryRow(
		`SELECT slug FROM aliases WHERE LOWER(alias) = ?`, strings.ToLower(strings.TrimSpace(name))).Scan(&slug)
	if err == nil && slug != "" {
		return Match{Slug: slug, Certain: true}, true
	}

	return Match{}, false
}

// NearMiss finds existing notes of the same kind with a similar name, for
// proposing a merge rather than creating a near-duplicate.
func NearMiss(db *sql.DB, name, kind string) []string {
	norm := vault.NormalizeLink(name)
	if len(norm) < 3 {
		return nil
	}

	rows, err := db.Query(`SELECT slug, title FROM notes WHERE kind = ?`, kind)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var slug, title string
		if rows.Scan(&slug, &title) != nil {
			continue
		}
		base := slug
		if i := strings.LastIndex(slug, "/"); i >= 0 {
			base = slug[i+1:]
		}
		if base == norm {
			continue // exact matches are Resolve's job
		}
		if similar(base, norm) || similar(vault.NormalizeLink(title), norm) {
			out = append(out, slug)
		}
	}
	return out
}

// similar catches the common shapes of the same name: one is a prefix of the
// other ("sam" / "sameer"), or they differ by a single edit ("sameer" /
// "sameir"). Cheap and predictable beats clever here — the user sees every
// match as a proposal, so recall matters more than precision.
func similar(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if strings.HasPrefix(a, b) || strings.HasPrefix(b, a) {
		return true
	}
	return editDistance(a, b) == 1
}

func editDistance(a, b string) int {
	if len(a) > len(b) {
		a, b = b, a
	}
	if len(b)-len(a) > 1 {
		return 2 // more than one edit apart; exact value does not matter
	}

	prev := make([]int, len(a)+1)
	for i := range prev {
		prev[i] = i
	}
	for j := 1; j <= len(b); j++ {
		cur := make([]int, len(a)+1)
		cur[0] = j
		for i := 1; i <= len(a); i++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[i] = min(min(cur[i-1]+1, prev[i]+1), prev[i-1]+cost)
		}
		prev = cur
	}
	return prev[len(a)]
}
