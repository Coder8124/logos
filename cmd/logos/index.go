package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Coder8124/logos/internal/dream"
	"github.com/Coder8124/logos/internal/index"
	"github.com/Coder8124/logos/internal/memory"
	"github.com/Coder8124/logos/internal/provider"
	"github.com/Coder8124/logos/internal/vault"
)

func runIndex(watch bool) error {
	ix, err := openIndexQuiet()
	if err != nil {
		return err
	}
	defer ix.Close()

	// A vault someone put under git must never be offered .logos/ to commit —
	// it is a rebuildable cache, and two people sharing a vault over git would
	// otherwise fight a merge conflict in a SQLite file on every pull — nor the
	// activity log, which is every command and file path a host reported. Runs
	// every time and reports only the run that actually changed something, so
	// `logos index` calling this on every invocation never turns into noise.
	if wrote, err := vault.EnsureGitignore(ix.Vault); err != nil {
		fmt.Fprintln(os.Stderr, "· could not update .gitignore:", err)
	} else if wrote {
		fmt.Println("· .gitignore now keeps .logos/ and activity/ out of git")
	}

	// Sync is pure file reading — it needs no model, and it is what keeps the
	// FTS table current. Only the embedding passes need a provider.
	//
	// Requiring one here left a hole in the middle of the no-runtime story:
	// lexical search worked, but the command that refreshes what it searches did
	// not, so editing a note on a machine without Ollama meant the change was
	// invisible until a model appeared. Worse, `logos checkpoint` tells the user
	// to run exactly this command.
	embed, embedOK := embedModel()
	p, perr := findProvider()
	switch {
	case !embedOK:
		fmt.Fprintln(os.Stderr,
			"· embeddings off (LOGOS_EMBED) — indexing text only")
		p = nil // the pass below keys the embedding work off a nil provider
	case perr != nil:
		fmt.Fprintln(os.Stderr,
			"· no model runtime — indexing text only; run this again with Ollama up to add embeddings")
	}

	pass := func() error { return indexPass(ix, os.Stdout, p, embed) }

	if err := pass(); err != nil || !watch {
		return err
	}

	fmt.Printf("watching %s …\n", ix.Vault)
	// Poll rather than fsnotify: the vault is small, a 2s tick is imperceptible,
	// and it sidesteps the editor-save event storms that make watchers fire
	// three times per file.
	for range time.Tick(2 * time.Second) {
		if err := pass(); err != nil {
			fmt.Fprintln(os.Stderr, "· sync error:", err)
		}
	}
	return nil
}

func search(query string) error {
	ix, err := openIndex()
	if err != nil {
		return err
	}
	defer ix.Close()

	// No runtime is not a failure: FTS5 is in the index either way, so fall back
	// to the lexical arm alone. Exact terms — names, error codes, IDs — are found
	// as well as they ever were; only paraphrase suffers.
	var hits []index.Hit
	if model, ok := embedModel(); !ok {
		fmt.Fprintln(os.Stderr, "· embeddings off (LOGOS_EMBED) — searching lexically")
		hits, err = ix.LexicalSearch(query, 8)
	} else if p, perr := findProvider(); perr == nil {
		hits, err = ix.HybridSearch(p, model, query, 8)
	} else {
		fmt.Fprintln(os.Stderr, "· no model runtime — searching lexically")
		hits, err = ix.LexicalSearch(query, 8)
	}
	if err != nil {
		return err
	}
	// Zero hits printed nothing at all, which reads the same as a crash: a
	// first-time user searching for a typo could not tell whether the command
	// had worked. `logos ask` already says so in words; match it.
	if len(hits) == 0 {
		fmt.Printf("Nothing in the vault matches %q yet.\n", query)
		return nil
	}
	for _, h := range hits {
		fmt.Printf("%.3f  %-28s %s\n", h.Score, h.Slug, h.Title)
	}
	return nil
}

func ask(question string) error {
	ix, err := openIndex()
	if err != nil {
		return err
	}
	defer ix.Close()

	p, err := findProvider()
	if err != nil {
		return err
	}

	// ask still needs a chat model to synthesise the answer; an empty embed
	// model (LOGOS_EMBED=off) only sends retrieval down the lexical arm inside
	// HybridSearch rather than 404ing "off" at the runtime.
	model, ok := embedModel()
	if !ok {
		fmt.Fprintln(os.Stderr, "· embeddings off (LOGOS_EMBED) — retrieving lexically")
	}
	answer, hits, err := ix.Ask(p, model,
		env("LOGOS_MODEL", defaultChatModel),
		question, 6, 6000)
	if err != nil {
		return err
	}

	fmt.Printf("\n%s\n\n", strings.TrimSpace(answer))
	fmt.Println("─── context ───")
	for _, h := range hits {
		if h.Via != "" {
			fmt.Printf("  %-28s via %s\n", h.Slug, h.Via)
		} else {
			fmt.Printf("  %-28s %.3f\n", h.Slug, h.Score)
		}
	}
	return nil
}

// indexPass is one rebuild of the cache from the vault, its report written to
// w: stdout for `logos index`, stderr for a rebuild auto runs ahead of another
// command whose stdout is its answer. A nil p indexes text only.
func indexPass(ix *index.Index, w io.Writer, p *provider.Provider, embed string) error {
	rep, err := ix.Sync()
	if err != nil {
		return err
	}
	notes, _ := ix.NoteCount()
	edges, _ := ix.EdgeCount()

	// Working notes come back before anything that needs a model, because
	// restoring them needs nothing but the file — and this is the command a
	// user runs after deleting the index, which is precisely when they are
	// gone. Announced when there were any: a rebuild that silently recovered
	// in-flight work is indistinguishable from one that lost it.
	restoredNotes, rescuedNotes, emptyNotes, err := ix.SyncNotes()
	if err != nil {
		fmt.Fprintln(os.Stderr, "· could not restore working notes:", err)
	}
	if restoredNotes > 0 {
		fmt.Fprintf(w, "restored %d uncommitted working %s\n", restoredNotes, plural(restoredNotes, "note"))
	}
	// Said, because the vault still has the line and the index now does not:
	// a reader comparing the two deserves to know it was on purpose.
	if emptyNotes > 0 {
		fmt.Fprintf(w, "left out %d working %s — only a kept-out <private> span or credential\n",
			emptyNotes, plural(emptyNotes, "note"))
	}
	// The other direction, and said out loud for the reason the rescued
	// proposals are: these were in the cache alone because a write to the
	// vault failed earlier, and the user was told that once, by a process
	// that has since exited. Silence here would make this run look like an
	// ordinary one while it repaired real data loss.
	if rescuedNotes > 0 {
		fmt.Fprintf(w, "wrote %d working %s to the vault — %s only in the index\n",
			rescuedNotes, plural(rescuedNotes, "note"), wasWere(rescuedNotes))
	}

	// Memories and the review queue come back with or without a model.
	// Import needs a provider only to re-embed, and passing a nil one skips
	// exactly that — so this used to sit behind the `p == nil` return
	// below, which meant a rebuild on a machine with no runtime restored
	// the notes and left every remembered fact out of the cache until some
	// later run happened to have Ollama up. "Delete the index, lose
	// nothing" cannot depend on a model being reachable.
	mems, rescuedMems, edits, err := ix.SyncMemoryEdits(p, embed)
	if err != nil {
		return err
	}
	printMemoryEdits(w, edits)
	// Same again for memories, and this is the count the bug was about: a
	// memory stranded in the cache used to be reaped here as a line the
	// user had deleted by hand, and reported under `-0`.
	if rescuedMems > 0 {
		fmt.Fprintf(w, "wrote %d memor%s to the vault — %s only in the index\n",
			rescuedMems, pluralY(rescuedMems), wasWere(rescuedMems))
	}

	// The review queue, after the memories, so an accepted proposal is
	// already an active memory before the queue is consulted about its id.
	if queued, rescued, err := ix.SyncPending(); err != nil {
		fmt.Fprintln(os.Stderr, "· could not restore the review queue:", err)
	} else {
		if queued > 0 {
			fmt.Fprintf(w, "restored %d memor%s awaiting review — run `logos review`\n",
				queued, pluralY(queued))
		}
		// Said out loud because it is a repair the user did not ask for and
		// would otherwise never know happened — and because it means their
		// queue was, until this run, one `rm -rf .logos` from gone.
		if rescued > 0 {
			fmt.Fprintf(w, "wrote %d memor%s awaiting review to the vault — they were only in the index\n",
				rescued, pluralY(rescued))
		}
	}

	// The timeline, after both. Announced because the alternative — a silent
	// repair — is how the old failure hid: `logos memory log` answered
	// confidently after a rebuild, with dates invented on the spot, and
	// nothing on stdout ever said the history had been touched.
	if events, err := ix.SyncLog(); err != nil {
		fmt.Fprintln(os.Stderr, "· could not restore the memory timeline:", err)
	} else if events > 0 {
		fmt.Fprintf(w, "restored %d memory %s — run `logos memory log`\n", events, plural(events, "event"))
	}

	// Open loops, which need no model either. Announced for the reason the
	// working notes are: an empty `logos loop` after a rebuild reads as a
	// list the user finished, not one the rebuild threw away. The count is
	// every loop put back, closed ones included — they are what stops a
	// dismissed commitment being extracted and surfaced all over again.
	if loops, rescuedLoops, err := ix.SyncLoops(); err != nil {
		fmt.Fprintln(os.Stderr, "· could not restore open loops:", err)
	} else {
		if loops > 0 {
			fmt.Fprintf(w, "restored %d tracked %s — run `logos memory loop`\n", loops, plural(loops, "loop"))
		}
		// The other direction, said for the reason the rescued memories are:
		// a write to the vault failed earlier, and the process told about it
		// has exited.
		if rescuedLoops > 0 {
			fmt.Fprintf(w, "wrote %d tracked %s to the vault — %s only in the index\n",
				rescuedLoops, plural(rescuedLoops, "loop"), wasWere(rescuedLoops))
		}
	}

	// Dreamed insights, after the memories they cite. Announced for the
	// reason the rest are: an empty `logos dream review` after a rebuild
	// reads as a queue the user has already been through, not one the
	// rebuild threw away. The count is every insight put back, reviewed
	// ones included — they are what stops a rejected connection being
	// proposed all over again.
	seen, rescuedInsights, err := ix.SyncInsights()
	if err != nil {
		fmt.Fprintln(os.Stderr, "· could not restore dreamed insights:", err)
	} else if rescuedInsights > 0 {
		fmt.Fprintf(w, "wrote %d dreamed %s to the vault — %s only in the index\n",
			rescuedInsights, plural(rescuedInsights, "insight"), wasWere(rescuedInsights))
	}
	if err == nil && seen > 0 {
		// Rejections are restored too — they are the record of what the user
		// already refused. Only point at the review command when there is
		// actually something waiting behind it.
		line := fmt.Sprintf("restored %d dreamed %s", seen, plural(seen, "insight"))
		if n, err := dream.PendingCount(ix.DB); err == nil && n > 0 {
			line += " — run `logos review --connections`"
		}
		fmt.Fprintln(w, line)
	}

	// Checkpoints whose own host_session frontmatter disagrees with the
	// conversation folder they are sitting in, last, since it needs no model
	// and touches nothing the counts above depend on. Announced for the same
	// reason every count above is: a rebuild that silently moved a file reads
	// as one that might have lost it.
	if refiled, err := ix.SyncCheckpointHomes(); err != nil {
		fmt.Fprintln(os.Stderr, "· could not re-file checkpoints:", err)
	} else if refiled > 0 {
		fmt.Fprintf(w, "re-filed %d checkpoint%s to match host_session, 0 lost\n", refiled, plural(refiled, ""))
	}

	printConflictCopies(w, "", rep.ConflictCopies, edits)

	// A killed write leaves its temp file behind for good, since the deferred
	// remove dies with the process. Removed here because index is the command
	// that already promises to repair what an interrupted write left; said
	// here because files vanishing from a synced folder unannounced reads as
	// the tool deleting things. The count is said even when a removal failed:
	// the files already gone are gone either way, and an error alone hid them.
	removed, rmErr := vault.RemoveTemps(ix.Vault, rep.StaleTemps)
	if len(removed) > 0 {
		fmt.Fprintf(w, "removed %d temp file%s left by an interrupted write: %s\n",
			len(removed), plural(len(removed), ""), strings.Join(removed, ", "))
	}
	if rmErr != nil {
		fmt.Fprintf(os.Stderr, "· could not remove %d temp file%s left by an interrupted write: %v\n",
			len(rep.StaleTemps)-len(removed), plural(len(rep.StaleTemps)-len(removed), ""), rmErr)
	}

	// A folder the walk could not enter is a folder of notes missing from
	// every count above; skipped, so one bad directory cannot stop the index,
	// but never silently.
	for _, u := range rep.Unreadable {
		fmt.Fprintf(os.Stderr, "· could not read %s — its notes are not indexed\n", terminalSafe(u, 400))
	}

	if p == nil {
		fmt.Fprintf(w, "+%d ~%d -%d =%d · %d notes, %d edges, %d memories · lexical only\n",
			rep.Added, rep.Updated, rep.Removed, rep.Unchanged, notes, edges, mems)
		return nil
	}

	embedded, err := ix.EmbedPending(p, embed, 32)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "+%d ~%d -%d =%d · embedded %d · %d notes, %d edges, %d memories\n",
		rep.Added, rep.Updated, rep.Removed, rep.Unchanged, embedded, notes, edges, mems)
	return nil
}

// runIndexAuto is `logos index --auto`, the rebuild the MCP server launches
// rather than run before its handshake. Under a lock and checked again, since
// every host's server starts at once and each one saw the index missing.
func runIndexAuto() error {
	ix, err := openIndexQuiet()
	if err != nil {
		return err
	}
	defer ix.Close()
	g, err := vault.Lock(ix.Vault, "auto-index")
	if err != nil {
		return err
	}
	defer g.Unlock()
	if synced, err := ix.Synced(); err != nil || synced {
		return err
	}
	rebuildIndex(ix)
	return nil
}

// printMemoryEdits says what a hand edit to memories/ did, which the summary
// line does not count. Both halves together are the case worth a hint: a line
// retyped without its `<!-- logos id=… -->` comment forgets the memory it was,
// project and confidence included, and comes back as a bare new one.
func printMemoryEdits(w io.Writer, e memory.Edits) {
	// Said once, the run it happens: a one-file-per-kind vault only ever
	// migrates once per kind, and silence here would be exactly the silent
	// background work invariant 3 rules out — the first `logos index` on an
	// old vault moves every memory into its own file, and the user has no
	// other way to know it happened or that nothing was lost doing it.
	if e.Migrated > 0 {
		fmt.Fprintf(w, "moved %d memor%s to one file each, 0 lost\n", e.Migrated, pluralY(e.Migrated))
	}
	if n := len(e.Added); n > 0 {
		fmt.Fprintf(w, "added %d memor%s from a line with no id comment in memories/ — %s\n",
			n, pluralY(n), memoryIDs(e.Added))
	}
	if n := len(e.Forgotten); n > 0 {
		fmt.Fprintf(w, "forgot %d memor%s whose line is gone from memories/ — %s\n",
			n, pluralY(n), memoryIDs(e.Forgotten))
	}
	if len(e.Added) > 0 && len(e.Forgotten) > 0 {
		fmt.Fprintln(w, "  (a line keeps its memory only while its `<!-- logos id=… -->` comment stays on it; a retyped line is a new memory, without the old one's project, confidence or pin)")
	}
	// Said every run while the line stays: it is not a memory, and the next
	// write to its file drops it, so this is the one place it is visible.
	for _, h := range e.Held {
		fmt.Fprintf(w, "left out of memory: %s\n", terminalSafe(h, 400))
	}
}

// printConflictCopies says the sync conflict copies an index left out of
// every count. A copy is neither read nor lost — it is on disk, waiting for
// someone to compare it with its original — so the counts alone would make it
// look like one or the other. An orphan, whose original memory is gone, is
// said apart: it is what a forget looks like after a sync client kept a copy,
// and the person decides whether that memory comes back.
func printConflictCopies(w io.Writer, prefix string, walked []string, e memory.Edits) {
	copies := append(append([]string{}, walked...), e.SetAside...)
	if n := len(copies); n > 0 {
		fmt.Fprintf(w, "%sset aside %d sync conflict %s, read the original instead — compare and delete: %s\n",
			prefix, n, plural2(n, "copy", "copies"), terminalSafe(strings.Join(copies, ", "), 2000))
	}
	if n := len(e.Orphaned); n > 0 {
		fmt.Fprintf(w, "%sset aside %d sync conflict %s of a memory that is no longer current — delete %s, or rename one to its original name to bring the memory back: %s\n",
			prefix, n, plural2(n, "copy", "copies"), plural2(n, "it", "them"), terminalSafe(strings.Join(e.Orphaned, ", "), 2000))
	}
}

func plural2(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// memoryIDs writes "#3, #4" — the handles `logos memory history` takes — and
// stops at five, since a whole file retyped is said well enough by its count.
func memoryIDs(ids []int64) string {
	var parts []string
	for i, id := range ids {
		if i == 5 {
			parts = append(parts, fmt.Sprintf("and %d more", len(ids)-5))
			break
		}
		parts = append(parts, fmt.Sprintf("#%d", id))
	}
	return strings.Join(parts, ", ")
}
