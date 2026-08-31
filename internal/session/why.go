package session

import (
	"path/filepath"
	"sort"
	"strings"
)

// Why a file is the way it is.
//
// `git blame` answers who changed a line and when. It cannot answer the question
// people actually have, which is *why* — the reasoning is in the pull request
// nobody kept, the thread that scrolled away, or the head of someone who left.
// So the same three approaches get re-attempted every eighteen months by people
// who have no way of knowing they were attempted before.
//
// Checkpoints already hold the answer. Every one records the decisions taken and
// the approaches ruled out, alongside the files that were touched while taking
// them. That is a join nobody was making: the data has been written since the
// first checkpoint and there was no way to ask it a question.
//
// The claim here is deliberately modest. This does not explain the code; it
// reports what was written down near it. A decision that nobody recorded is
// still invisible, and a file that was touched incidentally will show a decision
// it had little to do with. Saying "here is what was being decided when this
// file was last touched" is honest and useful. Saying "here is why this code
// exists" would be neither.

// A Mention is one checkpoint that touched a path, and what was being worked out
// at the time.
type Mention struct {
	Checkpoint
	// Matched is the path as the checkpoint recorded it, which is rarely
	// character-for-character what the caller asked about.
	Matched string
}

// Touching finds every checkpoint whose Files list refers to path, newest first.
//
// It searches every project rather than one, because the question "why is this
// file like this" does not come with a project attached — the caller has a path
// and nothing else. A file touched by two projects is a real situation and both
// answers are wanted.
func Touching(vaultDir, path string, limit int) ([]Mention, error) {
	projects, err := Projects(vaultDir)
	if err != nil {
		return nil, err
	}

	var out []Mention
	for _, project := range projects {
		// 0 means every checkpoint: a decision from two years ago is exactly the
		// kind of thing this exists to surface, so there is no window to apply.
		history, err := History(vaultDir, project, 0)
		if err != nil {
			continue // one unreadable project must not hide the rest
		}
		for _, c := range history {
			if matched, ok := mentions(c.Files, path); ok {
				out = append(out, Mention{Checkpoint: c, Matched: matched})
			}
		}
	}

	// Newest first. Checkpoint ids are lexically time-ordered, so the timestamp
	// is authoritative and the slug is the tiebreak for two in the same second.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].TS != out[j].TS {
			return out[i].TS > out[j].TS
		}
		return out[i].Slug > out[j].Slug
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// mentions decides whether a recorded file refers to the path being asked about.
//
// The matching is deliberately loose. An agent writes whatever it had in hand —
// `internal/memory/vaultstore.go`, `./internal/memory/vaultstore.go`,
// `vaultstore.go`, or a path relative to a directory nobody recorded. A user
// asks with whatever their shell completed. Requiring these to be equal would
// mean the feature almost never fires, and a lookup that usually returns nothing
// is one people stop running.
//
// So a suffix match on path segments counts, in either direction, and a bare
// filename matches any path ending in it. The cost of being generous is an
// occasional unrelated hit, which the caller can see and dismiss; the cost of
// being strict is silence, which they cannot.
func mentions(files []string, path string) (string, bool) {
	want := normalisePath(path)
	if want == "" {
		return "", false
	}
	wantBase := pathBase(want)

	for _, f := range files {
		got := normalisePath(f)
		if got == "" {
			continue
		}
		switch {
		case got == want:
			return f, true
		// One is a suffix of the other on a segment boundary: "memory/store.go"
		// asked, "internal/memory/store.go" recorded, or the reverse.
		case segmentSuffix(got, want) || segmentSuffix(want, got):
			return f, true
		// A bare filename on either side matches any path ending in it.
		case want == wantBase && pathBase(got) == wantBase:
			return f, true
		case got == pathBase(got) && pathBase(want) == got:
			return f, true
		}
	}
	return "", false
}

// segmentSuffix reports whether long ends with short at a path boundary, so
// "internal/memory/store.go" matches "memory/store.go" but not "ory/store.go".
func segmentSuffix(long, short string) bool {
	if len(long) <= len(short) {
		return false
	}
	return strings.HasSuffix(long, "/"+short)
}

// normalisePath strips the decorations that make two spellings of one file look
// different: leading ./, surrounding backticks or quotes from a markdown bullet,
// trailing slashes, and Windows separators.
func normalisePath(p string) string {
	p = strings.TrimSpace(p)
	p = strings.Trim(p, "`\"'")
	p = strings.ReplaceAll(p, "\\", "/")
	p = strings.TrimPrefix(p, "./")
	p = strings.TrimSuffix(p, "/")
	// A trailing comment or annotation — "store.go (rewritten)" — is common in a
	// hand-written bullet and would otherwise defeat every comparison.
	if i := strings.IndexAny(p, " \t("); i > 0 {
		p = p[:i]
	}
	return strings.TrimSpace(p)
}

func pathBase(p string) string {
	if p == "" {
		return ""
	}
	return filepath.Base(p)
}
