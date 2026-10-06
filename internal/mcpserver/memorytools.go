package mcpserver

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Coder8124/logos/internal/memory"
	"github.com/Coder8124/logos/internal/procedure"
	"github.com/Coder8124/logos/internal/secret"
	"github.com/Coder8124/logos/internal/untrusted"
)

// remember stores a fact scoped to the project the session is working on.
// global=true opts out, for the things that really do apply everywhere — a
// standing preference about how the user likes replies is not a fact about
// this repository.
func (s *Session) remember(text, kindStr, projectArg string, global bool) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("remember needs text")
	}
	project := ""
	if !global {
		project = s.resolveProject(projectArg)
	}
	kind := memory.Fact
	switch memory.Kind(kindStr) {
	case memory.Preference:
		kind = memory.Preference
	case memory.Person:
		kind = memory.Person
	case memory.Context:
		kind = memory.Context
	case memory.Procedure:
		kind = memory.Procedure
	}
	// A procedure earns its slot by naming what goes wrong without it — the
	// trap test. Refuse here, with the reason, rather than storing a
	// convention that will never be flagged as one again: a rejected write
	// must not come back looking like a stored one.
	if kind == memory.Procedure {
		if err := procedure.Validate(procedure.ParseRecord(text)); err != nil {
			return "", err
		}
	}
	r, err := memory.Store(s.DB, s.embed, s.embedModel, &memory.Memory{
		Text: text, Kind: kind, Salience: 0.7, Source: "mcp", Project: project, Agent: s.clientAgent,
		Quarantined:       reviewEverythingMCP(),
		ReviewIfContested: !trustMCP(),
	})
	if err != nil {
		return "", err
	}
	// Name the scope in the receipt. The host shows this to the user, and
	// "which pile did that go in" is the one thing they cannot otherwise see.
	where := "everywhere"
	if project != "" {
		where = project
	}
	msg := s.rememberReceipt(r, kind, where)
	if said := secret.Summary(r.Redactions); said != "" {
		msg += " " + said + "."
	}
	return msg + queueAdoption(r.QueueRestored, r.QueueRejected), nil
}

func (s *Session) rememberReceipt(r memory.Receipt, kind memory.Kind, where string) string {
	// A receipt rather than "Remembered." — the host is about to tell the user
	// what happened, and creating a fact is not the same as confirming one it
	// already had, or queuing one that still needs a yes.
	switch r.Outcome {
	case memory.EvReinforced:
		if r.StillQueued {
			return s.receipt(fmt.Sprintf("still queued — memory #%d (%s, %s) is waiting for review; the user runs `%s review` to accept or reject it", r.Ref, kind, where, s.shell()))
		}
		return s.receipt(fmt.Sprintf("already knew that — reinforced memory #%d (%s, %s)", r.Ref, kind, where))
	case memory.EvQuarantined:
		return s.receipt(s.quarantineReceipt(r.ID, string(kind), where, r))
	case memory.EvCreated:
		return s.receipt(fmt.Sprintf("stored in logos — memory #%d (%s, %s)", r.ID, kind, where))
	}
	return "Nothing stored."
}

// trustMCP and reviewEverythingMCP are the two ends of how much scrutiny a
// `remember` from an MCP client gets before it counts as known.
//
// The middle — the default — is that a write goes active unless it contradicts
// something already stored, and only the contradiction waits for a person. See
// the Review-only-what-is-in-dispute comment in memory.Store for why.
//
// This used to quarantine everything, on the reasoning that an MCP client is a
// different process and the user is not necessarily watching when it writes.
// The reasoning was right about the risk and wrong about the remedy, and the
// old comment here said so without following it: an agent whose every write
// silently queues has lost its memory just as thoroughly as one that writes
// with no oversight at all. MCP is not one path among several — it is the only
// path an agent has, so "review everything" meant nothing an agent learned ever
// reached the next agent unless the user personally typed `logos review`. What
// people install this for is continuity. A queue that has to be drained by hand
// before continuity happens is a bill most users will simply not pay, and the
// facts sit unreviewed while both agents behave as though nothing was stored.
//
// Both escape hatches stay, because the old default was right for someone:
//
//	LOGOS_TRUST_MCP=1    never queue, not even a contradiction
//	LOGOS_REVIEW_ALL=1   queue every agent write, as before
//
// LOGOS_* environment variables rather than a config file, matching every other
// one-bit decision in this codebase.
func trustMCP() bool { return os.Getenv("LOGOS_TRUST_MCP") != "" }

func reviewEverythingMCP() bool { return os.Getenv("LOGOS_REVIEW_ALL") != "" }

// recall searches this project's memories plus the global ones. allProjects
// widens it to everything, which is the "unless explicitly asked" half — an
// agent that genuinely wants another project's history can have it, but has to
// say so rather than getting it by accident.
func (s *Session) recall(query string, k int, projectArg string, allProjects bool) (string, error) {
	if strings.TrimSpace(query) == "" {
		return "", fmt.Errorf("recall needs a query")
	}
	var (
		mems []memory.Memory
		err  error
	)
	project := ""
	if !allProjects {
		project = s.resolveProject(projectArg)
	}
	if project == "" {
		mems, err = memory.Recall(s.DB, s.embed, s.embedModel, query, k)
	} else {
		mems, err = memory.RecallInProject(s.DB, s.embed, s.embedModel, query, project, k)
	}
	if err != nil {
		return "", err
	}
	if len(mems) == 0 {
		if project != "" {
			// A typo and a real project with nothing on the subject used to get
			// the same sentence, and the agent draws the same conclusion from
			// it: this work has no recorded facts, carry on without them. Only
			// one of those is true. resolveProject accepts any string, so the
			// check has to be here.
			if !s.projectExists(project) {
				return fmt.Sprintf("No project named %s in this vault%s", untrusted.Inline(project), s.knownProjectsSentence()) + s.awaitingReview(), nil
			}
			return fmt.Sprintf("No relevant memories in %s. Pass all_projects to search every project.", project) + s.awaitingReview(), nil
		}
		return "No relevant memories." + s.awaitingReview(), nil
	}
	var b strings.Builder
	// A memory the vault never got is still usable and still true — it is just
	// one `rm -rf .logos` from gone, and the README tells people that command
	// is safe. The write reported the failure once, to a caller that has since
	// exited; every reader after that saw a row indistinguishable from a
	// durable one. See memory.UnflushedIDs.
	stranded := memory.UnflushedIDs(s.DB)
	for _, m := range mems {
		// Tag anything from outside the current project, so a fact borrowed
		// from elsewhere cannot be read as this project's own settled truth.
		switch {
		// Inline, because each memory is one bullet and a stored fact may contain
		// anything: a newline plus "## Where we left off" turned a recalled fact
		// into a section of logos's own frame, with a "Next step" the reading
		// agent had no way to tell from the real one.
		case m.Project == "" || m.Project == project:
			fmt.Fprintf(&b, "- (%s%s) %s\n", m.Kind, notDurable(stranded[m.ID]), untrusted.Inline(m.Text))
		default:
			fmt.Fprintf(&b, "- (%s, from %s%s) %s\n", m.Kind, m.Project, notDurable(stranded[m.ID]), untrusted.Inline(m.Text))
		}
	}
	return strings.TrimRight(b.String(), "\n") + s.awaitingReview(), nil
}

// notDurable marks a memory that is in the cache and not in the vault. Worded
// as a fact about where it is rather than a warning, because the memory itself
// is fine — an agent should still use it, and should know not to rely on it
// being there tomorrow.
func notDurable(stranded bool) string {
	if !stranded {
		return ""
	}
	return ", not yet saved to the vault"
}

// fromProject names the project a memory belongs to when it is not the one the
// caller is standing in, and says nothing when it is — the same distinction
// recall draws, in the same words, so a reader moving between the two tools
// does not have to learn two conventions. A global fact belongs everywhere and
// is never foreign.
func fromProject(owner, here string) string {
	if owner == "" || owner == here {
		return ""
	}
	return ", from " + untrusted.Inline(owner)
}

// diffOwner is fromProject for the +/-/~ lines, which carry no kind to hang a
// clause off and so need their own parentheses.
func diffOwner(owner, here string) string {
	if owner == "" || owner == here {
		return ""
	}
	return " (" + untrusted.Inline(owner) + ")"
}

// listMemories lists every memory in the vault, labelled with the project each
// one belongs to. `here` is the project the caller is standing in, whose
// memories are printed bare; "" labels everything, which is what the resource
// surface wants because it is addressed to no one in particular.
//
// Labelled rather than scoped: this tool is meant to show everything, and that
// is fine as long as it says what everything is. It was not saying, and the
// consequence was worse than a model being misled — the documented use for this
// tool is "before forgetting something", `forget` takes the id printed here,
// and another project's id sat in the same undifferentiated list as this
// project's. The obvious next call deleted work from a repository nobody in the
// session had opened. recall has labelled foreign results since #155; these two
// tools are where that fix did not reach.
func (s *Server) listMemories(here string) (string, error) {
	mems, err := memory.All(s.DB)
	if err != nil {
		return "", err
	}
	if len(mems) == 0 {
		return "No memories yet.", nil
	}
	var b strings.Builder
	stranded := memory.UnflushedIDs(s.DB)
	for _, m := range mems {
		tag := ""
		// Pin state has to be visible here too, not just in the CLI — a host's
		// model deciding whether to pin/exclude something needs to see what
		// already is, or it will keep re-pinning the same memory every session.
		switch m.Pin {
		case memory.PinAlways:
			tag = " [pinned]"
		case memory.PinNever:
			tag = " [excluded]"
		}
		fmt.Fprintf(&b, "[%d] (%s%s%s)%s %s\n", m.ID, m.Kind, fromProject(m.Project, here), notDurable(stranded[m.ID]), tag, untrusted.Inline(m.Text))
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

func (s *Server) forget(idStr string) (string, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
	if err != nil {
		return "", fmt.Errorf("forget needs a numeric memory id")
	}
	if err := memory.Forget(s.DB, id); err != nil {
		return "", err
	}
	return "Forgotten.", nil
}

// pinMemory sets or clears always-include. unpin covers both directions of
// override (see memory.Unpin) so a host does not need a third tool just to
// walk back an exclude_memory call.
func (s *Server) pinMemory(idStr string, unpin bool) (string, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
	if err != nil {
		return "", fmt.Errorf("pin_memory needs a numeric memory id")
	}
	if unpin {
		if err := memory.Unpin(s.DB, id); err != nil {
			return "", err
		}
		return "Unpinned — back to normal ranking.", nil
	}
	if err := memory.Pin(s.DB, id); err != nil {
		return "", err
	}
	return "Pinned — always included in context packs, budget permitting.", nil
}

func (s *Server) excludeMemory(idStr string) (string, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
	if err != nil {
		return "", fmt.Errorf("exclude_memory needs a numeric memory id")
	}
	if err := memory.Exclude(s.DB, id); err != nil {
		return "", err
	}
	return "Excluded — kept on record, never surfaced.", nil
}

// memoryDiff reports what the memory learned, dropped, or corroborated over the
// last `days`, optionally about one subject. Instant and offline — it reads the
// append-only memory log, no model.
func (s *Server) memoryDiff(subject string, days int, here string) (string, error) {
	if days <= 0 {
		days = 7
	}
	until := time.Now()
	since := until.AddDate(0, 0, -days)
	res, err := memory.Diff(s.DB, subject, since.Unix(), until.Unix())
	if err != nil {
		return "", err
	}
	if res.Empty() {
		return "Nothing changed in that window.", nil
	}
	var b strings.Builder
	// One line per entry, for the same reason recall collapses: the +/-/~ marker
	// is the only thing distinguishing logos's reading of the window from the
	// stored text beside it.
	// The project on each line for the same reason recall carries it: over a
	// window, every project's changes arrive in one list, and an unlabelled
	// line about another repository reads as a change to this one.
	for _, e := range res.Added {
		fmt.Fprintf(&b, "+%s %s\n", diffOwner(e.Project, here), untrusted.Inline(e.Text))
	}
	for _, e := range res.Removed {
		fmt.Fprintf(&b, "-%s %s\n", diffOwner(e.Project, here), untrusted.Inline(e.Text))
	}
	for _, e := range res.Corroborated {
		fmt.Fprintf(&b, "~%s %s\n", diffOwner(e.Project, here), untrusted.Inline(e.Text))
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// quarantineReceipt names the review command this install answers to. Under
// npx or the plugin alone there is no logos on PATH, and "run `logos review`"
// left the memory queued behind a command the user could not run.
func (s *Server) quarantineReceipt(id int64, kind, where string, r memory.Receipt) string {
	// Why it queued, not just that it did. A memory only waits for review when
	// it disputes one already stored, so the receipt quotes the memory in
	// dispute — that is what lets the agent raise it in the conversation the
	// user is already having, rather than leaving it for a queue they open
	// some other day.
	if r.Contested != 0 {
		return fmt.Sprintf("queued memory #%d (%s, %s) — it contradicts memory #%d, %q. The user runs `%s review` to settle which is current; until then neither answer changes",
			id, kind, where, r.Contested, r.ContestedText, s.shell())
	}
	return fmt.Sprintf("queued memory #%d (%s, %s) for review — the user runs `%s review` to accept or reject it before it becomes active", id, kind, where, s.shell())
}

// shell is the command this install answers to; see quarantineReceipt.
func (s *Server) shell() string {
	if s.Shell == "" {
		return "logos"
	}
	return s.Shell
}

// awaitingReview is the line every read appends while the review queue is not
// empty. Quarantine keeps an agent's memories out of recall until the user says
// yes, and a user who is never told there is anything to say yes to leaves them
// there for good — while the agent reads the empty recall as the fact never
// having been stored. A failed count is said, not swallowed, but does not fail
// the read it is attached to.
func (s *Server) awaitingReview() string {
	// Adopt hand edits to the queue file first, as `logos review` does: a
	// proposal whose line the user deleted is one they rejected, and counting
	// it asks them to review it again. Said, because the read just discarded
	// something.
	restored, rejected, err := memory.ReconcilePending(s.DB)
	if err != nil {
		return fmt.Sprintf("\n\n(could not read the user's edits to the review queue: %v)", err) + s.pendingLine()
	}
	return queueAdoption(restored, rejected) + s.pendingLine()
}

// queueAdoption says what a hand edit to the review queue file did to the
// queue, in the words `logos review` prints for the same adoption. Empty when
// nothing changed, which is nearly always.
func queueAdoption(restored, rejected int) string {
	var parts []string
	if rejected > 0 {
		parts = append(parts, fmt.Sprintf("%d %s rejected", rejected, proposals(rejected)))
	}
	if restored > 0 {
		parts = append(parts, fmt.Sprintf("%d %s taken from the file", restored, proposals(restored)))
	}
	if len(parts) == 0 {
		return ""
	}
	return fmt.Sprintf("\n\nadopted the user's edit to %s/%s: %s", memory.Dir, memory.PendingFile, strings.Join(parts, ", "))
}

func proposals(n int) string {
	if n == 1 {
		return "proposal"
	}
	return "proposals"
}

// pendingLine is the count half of awaitingReview.
func (s *Server) pendingLine() string {
	n, err := memory.PendingCount(s.DB)
	if err != nil {
		return fmt.Sprintf("\n\n(could not count the memories waiting for review: %v)", err)
	}
	if n == 0 {
		return ""
	}
	if n == 1 {
		return fmt.Sprintf("\n\n1 memory is waiting for your review — `%s review`", s.shell())
	}
	return fmt.Sprintf("\n\n%d memories are waiting for your review — `%s review`", n, s.shell())
}
