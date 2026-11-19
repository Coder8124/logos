package deadend

import (
	"database/sql"
	"sort"
	"strings"

	"github.com/Coder8124/logos/internal/textmatch"
)

// before_you_try waits for an agent to think of asking, and the edit hook
// waits for a file. Neither fires at the moment a person types "let's try
// streaming the vendor export" — the one moment the ruling against it would
// change what happens next, before an agent has planned anything.
//
// A prompt is not a proposal, and that changes the matching. before_you_try's
// containment divides by the shorter side, which here cuts both ways: a long
// prompt contains most of any short route by vocabulary alone, and a short
// clause ("merge the bugfix branches") is two-thirds contained in any long
// ruling that mentions merging branches. Scored that way, a third of this
// project's real prompts were shown a ruling. So the prompt is matched a clause
// at a time, each clause standing in for one proposal, and the score is how
// much of the ruling's route one clause covers: a restatement says most of
// what was ruled out, not a few of its words. And it is held to the project
// the prompt was typed in: before_you_try flags a ruling from elsewhere as
// elsewhere, but nobody asked for this one, and an uninvited line about
// another codebase is noise the agent learns to skip.

// routeCoverage is how much of a ruling's route one clause has to say before
// it reads as a restatement. Set against 300 of this repository's own past
// prompts: at 0.75 six were shown a ruling, two of them true restatements; at
// two-thirds it was fifteen, because two words of a three-word route ("merge
// outstanding branches") is every request to merge anything. The price is a
// loose paraphrase — "file each fact" for "filing facts" — which
// before_you_try still catches when the agent asks.
const routeCoverage = 0.75

// promptClauseLimit caps how much of a pasted prompt is read. A prompt with a
// log pasted into it is mostly not the user's intent, and scoring every line
// of it against every ruling is the part of the hook that grows without bound.
// The last clauses are the ones kept: people paste the log first and type
// what they want under it.
const promptClauseLimit = 40

// AtPrompt returns the rulings in project and its worktree scopes that one
// clause of prompt restates, best first, at most k. Lexical only: a prompt
// hook cannot wait on an embedding model. A ruling marked possibly superseded
// is left out — before_you_try shows it with a caveat, but a caveat on a line
// nobody asked for is a line arguing with itself.
func AtPrompt(vaultDir string, db *sql.DB, project, prompt string, k int) ([]Ruling, error) {
	if strings.TrimSpace(project) == "" || strings.TrimSpace(prompt) == "" {
		return nil, nil
	}
	clauses := promptClauses(prompt)
	if len(clauses) == 0 {
		return nil, nil
	}
	scopes, err := scopesOf(vaultDir, project)
	if err != nil {
		return nil, err
	}
	var hits []Ruling
	seen := map[string]bool{}
	for _, s := range scopes {
		rulings, err := Collect(vaultDir, db, s)
		if err != nil {
			return nil, err
		}
		for _, r := range rulings {
			// The same route ruled out twice (a checkpoint and the note it was
			// promoted from) is one ruling to the reader.
			key := strings.ToLower(strings.Join(strings.Fields(r.Text), " "))
			if r.Stale || seen[key] {
				continue
			}
			subject := textmatch.Subject(r.Text)
			// The guard before_you_try uses: one shared word is not the same
			// approach, however short the ruling.
			if len(subject) < 2 {
				continue
			}
			best := 0.0
			for _, c := range clauses {
				n := sharedWords(c, subject)
				if n < 2 {
					continue
				}
				if o := float64(n) / float64(len(subject)); o > best {
					best = o
				}
			}
			if best < routeCoverage {
				continue
			}
			seen[key] = true
			r.Method, r.Score = MatchedLexically, best
			hits = append(hits, r)
		}
	}
	sort.SliceStable(hits, func(a, b int) bool {
		if hits[a].Score != hits[b].Score {
			return hits[a].Score > hits[b].Score
		}
		return hits[a].When > hits[b].When
	})
	if k > 0 && len(hits) > k {
		hits = hits[:k]
	}
	return hits, nil
}

// promptClauses splits a prompt into the subjects of its sentences and lines.
// A clause of fewer than two distinctive words cannot pass the shared-words
// guard and is dropped here rather than scored.
func promptClauses(prompt string) []map[string]bool {
	parts := strings.FieldsFunc(prompt, func(r rune) bool {
		switch r {
		// Commas too: a list of requests run together with commas is still
		// separate requests, and read as one clause their words add up to a
		// restatement none of them makes.
		case '\n', '.', '?', '!', ';', ',':
			return true
		}
		return false
	})
	var out []map[string]bool
	for _, p := range parts {
		if s := textmatch.Subject(p); len(s) >= 2 {
			out = append(out, s)
		}
	}
	if len(out) > promptClauseLimit {
		out = out[len(out)-promptClauseLimit:]
	}
	return out
}
