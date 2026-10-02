package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Coder8124/logos/internal/dream"
	"github.com/Coder8124/logos/internal/index"
	"github.com/Coder8124/logos/internal/vault"
)

func runIndex(watch bool) error {
	ix, err := openIndex()
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

	pass := func() error {
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
		restoredNotes, rescuedNotes, err := ix.SyncNotes()
		if err != nil {
			fmt.Fprintln(os.Stderr, "· could not restore working notes:", err)
		}
		if restoredNotes > 0 {
			fmt.Printf("restored %d uncommitted working %s\n", restoredNotes, plural(restoredNotes, "note"))
		}
		// The other direction, and said out loud for the reason the rescued
		// proposals are: these were in the cache alone because a write to the
		// vault failed earlier, and the user was told that once, by a process
		// that has since exited. Silence here would make this run look like an
		// ordinary one while it repaired real data loss.
		if rescuedNotes > 0 {
			fmt.Printf("wrote %d working %s to the vault — %s only in the index\n",
				rescuedNotes, plural(rescuedNotes, "note"), wasWere(rescuedNotes))
		}

		// Memories and the review queue come back with or without a model.
		// Import needs a provider only to re-embed, and passing a nil one skips
		// exactly that — so this used to sit behind the `p == nil` return
		// below, which meant a rebuild on a machine with no runtime restored
		// the notes and left every remembered fact out of the cache until some
		// later run happened to have Ollama up. "Delete the index, lose
		// nothing" cannot depend on a model being reachable.
		mems, rescuedMems, err := ix.SyncMemories(p, embed)
		if err != nil {
			return err
		}
		// Same again for memories, and this is the count the bug was about: a
		// memory stranded in the cache used to be reaped here as a line the
		// user had deleted by hand, and reported under `-0`.
		if rescuedMems > 0 {
			fmt.Printf("wrote %d memor%s to the vault — %s only in the index\n",
				rescuedMems, pluralY(rescuedMems), wasWere(rescuedMems))
		}

		// The review queue, after the memories, so an accepted proposal is
		// already an active memory before the queue is consulted about its id.
		if queued, rescued, err := ix.SyncPending(); err != nil {
			fmt.Fprintln(os.Stderr, "· could not restore the review queue:", err)
		} else {
			if queued > 0 {
				fmt.Printf("restored %d memor%s awaiting review — run `logos review`\n",
					queued, pluralY(queued))
			}
			// Said out loud because it is a repair the user did not ask for and
			// would otherwise never know happened — and because it means their
			// queue was, until this run, one `rm -rf .logos` from gone.
			if rescued > 0 {
				fmt.Printf("wrote %d memor%s awaiting review to the vault — they were only in the index\n",
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
			fmt.Printf("restored %d memory %s — run `logos memory log`\n", events, plural(events, "event"))
		}

		// Open loops, which need no model either. Announced for the reason the
		// working notes are: an empty `logos loop` after a rebuild reads as a
		// list the user finished, not one the rebuild threw away. The count is
		// every loop put back, closed ones included — they are what stops a
		// dismissed commitment being extracted and surfaced all over again.
		if loops, err := ix.SyncLoops(); err != nil {
			fmt.Fprintln(os.Stderr, "· could not restore open loops:", err)
		} else if loops > 0 {
			fmt.Printf("restored %d tracked %s — run `logos loop`\n", loops, plural(loops, "loop"))
		}

		// Dreamed insights, after the memories they cite. Announced for the
		// reason the rest are: an empty `logos dream review` after a rebuild
		// reads as a queue the user has already been through, not one the
		// rebuild threw away. The count is every insight put back, reviewed
		// ones included — they are what stops a rejected connection being
		// proposed all over again.
		if seen, err := ix.SyncInsights(); err != nil {
			fmt.Fprintln(os.Stderr, "· could not restore dreamed insights:", err)
		} else if seen > 0 {
			// Rejections are restored too — they are the record of what the user
			// already refused. Only point at the review command when there is
			// actually something waiting behind it.
			line := fmt.Sprintf("restored %d dreamed %s", seen, plural(seen, "insight"))
			if n, err := dream.PendingCount(ix.DB); err == nil && n > 0 {
				line += " — run `logos dream review`"
			}
			fmt.Println(line)
		}

		if p == nil {
			fmt.Printf("+%d ~%d -%d =%d · %d notes, %d edges, %d memories · lexical only\n",
				rep.Added, rep.Updated, rep.Removed, rep.Unchanged, notes, edges, mems)
			return nil
		}

		embedded, err := ix.EmbedPending(p, embed, 32)
		if err != nil {
			return err
		}
		fmt.Printf("+%d ~%d -%d =%d · embedded %d · %d notes, %d edges, %d memories\n",
			rep.Added, rep.Updated, rep.Removed, rep.Unchanged, embedded, notes, edges, mems)
		return nil
	}

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
