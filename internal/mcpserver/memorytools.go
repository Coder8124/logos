package mcpserver

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Coder8124/logos/internal/memory"
	"github.com/Coder8124/logos/internal/ops"
	"github.com/Coder8124/logos/internal/untrusted"
)

// remember stores a fact scoped to the project the session is working on.
// global=true opts out, for the things that really do apply everywhere — a
// standing preference about how the user likes replies is not a fact about
// this repository.
// remember also reports whether a memory now exists, which is not the same as
// err == nil: one the vault refused is a tool error, and still in the cache.
func (s *Session) remember(text, kindStr, projectArg string, global bool) (string, bool, error) {
	if strings.TrimSpace(text) == "" {
		return "", false, fmt.Errorf("remember needs text")
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
	res, err := ops.Remember(s.DB, ops.RememberRequest{
		Memory: memory.Memory{
			Text: text, Kind: kind, Salience: 0.7, Source: "mcp", Project: project, Agent: s.clientAgent,
			Quarantined:       reviewEverythingMCP(),
			ReviewIfContested: !trustMCP(),
		},
		Embed: s.embed, Model: s.embedModel,
	})
	if err != nil {
		return "", false, err
	}
	// Name the scope in the receipt. The host shows this to the user, and
	// "which pile did that go in" is the one thing they cannot otherwise see.
	where := "everywhere"
	if project != "" {
		where = project
	}
	what := s.rememberReceipt(res.Receipt, kind, where)
	switch {
	case what == "":
		return "Nothing stored." + res.Outcome.Text(), false, nil
	case res.Outcome.Failed():
		// A tool error, so the host does not read a half-done write as done —
		// but with the receipt in it, which is what the agent needs to act on
		// it. Without the announcement badge, which the user reads as "done".
		return "", true, errors.New(upperFirst(what) + res.Outcome.Text())
	}
	return s.receipt(what) + res.Outcome.Text(), true, nil
}

func (s *Session) rememberReceipt(r memory.Receipt, kind memory.Kind, where string) string {
	// A receipt rather than "Remembered." — the host is about to tell the user
	// what happened, and creating a fact is not the same as confirming one it
	// already had, or queuing one that still needs a yes. Unbadged, and empty
	// when nothing was stored: the caller decides whether it reads as done.
	switch r.Outcome {
	case memory.EvReinforced:
		if r.StillQueued {
			return fmt.Sprintf("still queued — memory #%d (%s, %s) is waiting for review; the user runs `%s review` to accept or reject it", r.Ref, kind, where, s.shell())
		}
		return fmt.Sprintf("already knew that — reinforced memory #%d (%s, %s)", r.Ref, kind, where)
	case memory.EvQuarantined:
		return s.quarantineReceipt(r.ID, string(kind), where, r)
	case memory.EvCreated:
		return fmt.Sprintf("stored in logos — memory #%d (%s, %s)", r.ID, kind, where)
	}
	return ""
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
	project := ""
	if !allProjects {
		project = s.resolveProject(projectArg)
	}
	r, err := ops.Recall(s.DB, ops.RecallQuery{
		Query: query, Limit: k, Project: project,
		Embed: s.embed, Model: s.embedModel, Shell: s.shell(),
	})
	if err != nil {
		return "", err
	}
	ifEmpty := "No relevant memories."
	if project != "" {
		// A typo and a real project with nothing on the subject used to get
		// the same sentence, and the agent draws the same conclusion from
		// it: this work has no recorded facts, carry on without them. Only
		// one of those is true. resolveProject accepts any string, so the
		// check has to be here.
		ifEmpty = fmt.Sprintf("No relevant memories in %s. Pass all_projects to search every project.", project)
		if len(r.Memories) == 0 && !s.projectExists(project) {
			ifEmpty = fmt.Sprintf("No project named %s in this vault%s", untrusted.Inline(project), s.knownProjectsSentence())
		}
	}
	return r.Text(ifEmpty), nil
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
		fmt.Fprintf(&b, "[%d] (%s%s%s)%s %s\n", m.ID, m.Kind, fromProject(m.Project, here), ops.NotDurable(stranded[m.ID]), tag, untrusted.Inline(m.Text))
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

// awaitingReview is the review queue's report, appended to every read. See
// ops.ReviewQueue.
func (s *Server) awaitingReview() string {
	return ops.ReviewQueue(s.DB, s.shell()).Text()
}
