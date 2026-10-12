package ops

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"github.com/Coder8124/logos/internal/memory"
	"github.com/Coder8124/logos/internal/provider"
	"github.com/Coder8124/logos/internal/session"
	"github.com/Coder8124/logos/internal/text"
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
	// Dir is the repository the asker stands in, or "". A memory recorded at
	// one of its commits is measured against it, and marked when a file it
	// names has changed since.
	Dir string
	// Compact returns an id and one line per memory instead of the whole
	// text, so an agent surveying what is known pays only for the memories it
	// then asks for by id (RecallIDs).
	Compact bool
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
	Compact  bool       `json:"compact,omitempty"`
}

// Recall answers a query from memory and reports the review queue alongside,
// which it adopts the user's hand edits to on the way.
func Recall(db *sql.DB, q RecallQuery) (RecallResult, error) {
	if strings.TrimSpace(q.Query) == "" {
		return RecallResult{}, fmt.Errorf("recall needs a query, or ids to fetch in full")
	}
	// A deleted index opens empty, with no memory tables, and recall is often
	// the first thing run after it; without this the user was shown SQLite's
	// "no such table" instead of an empty answer.
	if err := memory.Init(db); err != nil {
		return RecallResult{}, err
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
	memory.MarkDrift(mems, q.Dir)
	r := RecallResult{Project: q.Project, Compact: q.Compact}
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
	if r.Compact {
		return r.compactText()
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
			fmt.Fprintf(&b, "- (%s%s) %s%s\n", m.Kind, NotDurable(m.NotDurable), untrusted.Inline(m.Text), m.DriftNote())
		default:
			fmt.Fprintf(&b, "- (%s, from %s%s) %s%s\n", m.Kind, m.Project, NotDurable(m.NotDurable), untrusted.Inline(m.Text), m.DriftNote())
		}
	}
	return strings.TrimRight(b.String(), "\n") + r.Outcome.Text()
}

// compactTitle is how long a compact line's text runs before it is cut. Long
// enough to tell two memories on one subject apart, short enough that ten of
// them cost less than one memory served whole.
const compactTitle = 80

func (r RecallResult) compactText() string {
	var b strings.Builder
	ids := make([]string, 0, len(r.Memories))
	for _, m := range r.Memories {
		from := ""
		if m.Project != "" && m.Project != r.Project {
			from = ", from " + untrusted.Inline(m.Project)
		}
		fmt.Fprintf(&b, "- #%d (%s%s%s) %s%s\n", m.ID, m.Kind, from, NotDurable(m.NotDurable),
			text.Ellipsize(untrusted.Inline(m.Text), compactTitle), m.DriftNote())
		ids = append(ids, fmt.Sprint(m.ID))
	}
	// Said every time rather than left to the tool description: the agent
	// reading a cut line is the one that has to know the rest is one call away.
	fmt.Fprintf(&b, "Lines are cut at %d characters. For the full text, recall with ids: %s", compactTitle, strings.Join(ids, ","))
	return b.String() + r.Outcome.Text()
}

// RecallIDs serves memories by the ids a compact recall (or list_memories)
// printed. here is the project the asker stands in, for labelling another
// project's memory as such, and dir the repository it stands in, for the same
// drift mark a search gives. Every id gets a line: one that is gone, replaced,
// waiting for review or excluded says which, because an id that silently
// returns nothing reads as a memory that never existed.
func RecallIDs(db *sql.DB, ids []string, here, dir string) (string, error) {
	if err := memory.Init(db); err != nil {
		return "", err
	}
	// notIDs[i] is said just before the i-th number's answer, so the reply
	// lines up with the list the agent sent.
	var (
		b       strings.Builder
		numbers []int64
		notIDs  = map[int][]string{}
	)
	for _, raw := range ids {
		raw = strings.TrimPrefix(strings.TrimSpace(raw), "#")
		if raw == "" {
			continue
		}
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			notIDs[len(numbers)] = append(notIDs[len(numbers)],
				fmt.Sprintf("- %s is not a memory id; ids are the numbers after # in a compact recall\n", untrusted.Inline(raw)))
			continue
		}
		numbers = append(numbers, n)
	}
	if len(numbers) == 0 && len(notIDs) == 0 {
		return "", fmt.Errorf("recall needs a query or ids")
	}
	got, err := memory.ByIDs(db, numbers)
	if err != nil {
		return "", err
	}
	stranded := memory.UnflushedIDs(db)
	served := make([]memory.Memory, len(got))
	for i := range got {
		served[i] = got[i].Memory
	}
	memory.MarkDrift(served, dir)
	for i := range got {
		got[i].Memory = served[i]
	}
	for i, f := range got {
		b.WriteString(strings.Join(notIDs[i], ""))
		m := f.Memory
		switch {
		case !f.Found:
			fmt.Fprintf(&b, "- #%d: No memory with this id; it may have been forgotten\n", f.ID)
		case f.Superseded && m.SupersededBy != 0:
			fmt.Fprintf(&b, "- #%d: replaced by #%d; recall that id for what is believed now\n", f.ID, m.SupersededBy)
		case f.Superseded:
			fmt.Fprintf(&b, "- #%d: superseded, and no longer believed\n", f.ID)
		case m.Quarantined:
			fmt.Fprintf(&b, "- #%d: waiting in the review queue, not yet trusted, so not served\n", f.ID)
		case m.Pin == memory.PinNever:
			fmt.Fprintf(&b, "- #%d: excluded by the user, so not served\n", f.ID)
		default:
			from := ""
			if m.Project != "" && m.Project != here {
				from = ", from " + untrusted.Inline(m.Project)
			}
			fmt.Fprintf(&b, "- #%d (%s%s%s) %s%s\n", m.ID, m.Kind, from, NotDurable(stranded[m.ID]), untrusted.Inline(m.Text), m.DriftNote())
		}
	}
	b.WriteString(strings.Join(notIDs[len(got)], ""))
	return strings.TrimRight(b.String(), "\n"), nil
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
