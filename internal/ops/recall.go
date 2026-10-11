package ops

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/Coder8124/logos/internal/memory"
	"github.com/Coder8124/logos/internal/provider"
	"github.com/Coder8124/logos/internal/session"
	"github.com/Coder8124/logos/internal/untrusted"
)

// RecallQuery is a recall as any surface asks it. Project "" searches every
// project; otherwise the project's memories plus the global ones.
type RecallQuery struct {
	Query   string
	Limit   int
	Project string
	Embed   *provider.Provider // nil ranks by keyword alone
	Model   string
	Shell   string
}

// Recalled is a memory as recall returns it, with where it is kept.
type Recalled struct {
	memory.Memory
	// NotDurable marks a memory the cache holds and the vault does not. It is
	// still usable and still true — it is just one `rm -rf .logos` from gone,
	// and the README tells people that command is safe. The write reported the
	// failure once, to a caller that has since exited; every reader after
	// that saw a row indistinguishable from a durable one. See
	// memory.UnflushedIDs.
	NotDurable bool `json:"not_durable,omitempty"`
}

type RecallResult struct {
	Memories []Recalled `json:"memories"`
	Project  string     `json:"project,omitempty"`
	Outcome  Outcome    `json:"outcome"`
}

// Recall answers a query from memory and reports the review queue alongside,
// which it adopts the user's hand edits to on the way.
func Recall(db *sql.DB, q RecallQuery) (RecallResult, error) {
	if strings.TrimSpace(q.Query) == "" {
		return RecallResult{}, fmt.Errorf("recall needs a query")
	}
	var (
		mems []memory.Memory
		err  error
	)
	if q.Project == "" {
		mems, err = memory.Recall(db, q.Embed, q.Model, q.Query, q.Limit)
	} else {
		mems, err = memory.RecallInProject(db, q.Embed, q.Model, q.Query, q.Project, q.Limit)
	}
	if err != nil {
		return RecallResult{}, err
	}
	r := RecallResult{Project: q.Project}
	if len(mems) > 0 {
		stranded := memory.UnflushedIDs(db)
		for _, m := range mems {
			r.Memories = append(r.Memories, Recalled{Memory: m, NotDurable: stranded[m.ID]})
		}
	}
	r.Outcome = ReviewQueue(db, q.Shell)
	return r, nil
}

// Text renders the result, with ifEmpty standing in for the list when nothing
// matched — the one sentence a surface words for itself, since only it knows
// what its caller can do about it (a flag, a tool parameter).
func (r RecallResult) Text(ifEmpty string) string {
	if len(r.Memories) == 0 {
		return ifEmpty + r.Outcome.Text()
	}
	var b strings.Builder
	for _, m := range r.Memories {
		// Tag anything from outside the current project, so a fact borrowed
		// from elsewhere cannot be read as this project's own settled truth.
		// Inline, because each memory is one bullet and a stored fact may
		// contain anything: a newline plus "## Where we left off" turned a
		// recalled fact into a section of logos's own frame, with a "Next
		// step" the reading agent had no way to tell from the real one.
		switch {
		case m.Project == "" || m.Project == r.Project:
			fmt.Fprintf(&b, "- (%s%s) %s\n", m.Kind, NotDurable(m.NotDurable), untrusted.Inline(m.Text))
		default:
			fmt.Fprintf(&b, "- (%s, from %s%s) %s\n", m.Kind, m.Project, NotDurable(m.NotDurable), untrusted.Inline(m.Text))
		}
	}
	return strings.TrimRight(b.String(), "\n") + r.Outcome.Text()
}

// NotDurable is worded as a fact about where a memory is rather than a
// warning, because the memory itself is fine — an agent should still use it,
// and should know not to rely on it being there tomorrow.
func NotDurable(stranded bool) string {
	if !stranded {
		return ""
	}
	return ", not yet saved to the vault"
}

// ProjectExists reports whether the vault has ever heard this exact name —
// either a memory filed under it or a session directory carrying it. Both are
// consulted because a project can have checkpoints and no memory, or memory
// and no checkpoint, and either one makes the name real.
func ProjectExists(db *sql.DB, vault, name string) bool {
	if ok, err := memory.HasProject(db, name); err == nil && ok {
		return true
	}
	names, err := session.Scopes(vault)
	if err != nil {
		return false
	}
	for _, n := range names {
		// Either direction counts: a worktree scope is "shop/fix-auth" while
		// the enumerator lists "shop", so a name can be the parent of a known
		// scope or a scope under a known parent.
		if n == name || strings.HasPrefix(n, name+"/") || strings.HasPrefix(name, n+"/") {
			return true
		}
	}
	return false
}
