package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Coder8124/logos/internal/health"
	"github.com/Coder8124/logos/internal/index"
	"github.com/Coder8124/logos/internal/provider"
	"github.com/Coder8124/logos/internal/session"
	"github.com/Coder8124/logos/internal/vault"
)

// chooseVault resolves where the vault lives and, unless this is a dry run,
// makes sure it exists and is the one this machine remembers.
//
// created reports whether the directory was missing, so a dry run can say what
// it would have made without making it. recorded reports whether this vault was
// written down as the machine's, which is not the same question.
//
// A vault named only by LOGOS_VAULT is deliberately not recorded. LOGOS_VAULT
// is a per-process override — it is how the documented scratch-vault workflow
// works, and how an MCP host config pins one server to one vault — so treating
// it as a machine-wide choice means a single `setup` run against a throwaway
// directory silently repoints every front end at it. That shipped: a scratch
// vault under an agent's job directory became the recorded pointer, and because
// the directory still existed, Recorded() kept returning it. Every command, the
// MCP server and the SessionStart hook then read an empty vault and truthfully
// reported nothing, while twenty-eight checkpoints sat in ~/logos. --vault, and
// the default, are choices someone made; an inherited environment variable is
// not.
// recorded says whether this vault became the machine's recorded pointer, and
// why not when it did not. The reason is load-bearing: the three ways to end up
// unrecorded — the environment chose the vault, the write failed, or this was a
// dry run — need three different next steps, and reporting one of them for all
// three told a user whose config directory was unwritable to "pass --vault",
// which is exactly what they had just done.
type recordOutcome int

const (
	recordedHere   recordOutcome = iota // written down
	recordSkipEnv                       // LOGOS_VAULT chose it, so it is this process only
	recordFailed                        // the write was attempted and failed; the error is already printed
	recordSkipTemp                      // a temporary directory, and nobody said to record it anyway
	recordSkipMove                      // this machine already has a vault holding work, and nobody said to move it
)

func chooseVault(args []string, dryRun bool) (dir string, created bool, rec recordOutcome, err error) {
	dir = flagStr(args, "--vault", "")
	fromEnv := false
	if dir == "" {
		if v := os.Getenv("LOGOS_VAULT"); v != "" {
			// fromEnv means somebody named a vault for this one run. The host
			// pin this process adopted is not that: it is the machine's own
			// recorded choice arriving by another road, and treating it as
			// per-process made setup inside a host record nothing.
			dir, fromEnv = v, !vaultCameFromHostPin(v)
		} else {
			dir = vaultPath() // the recorded path, then ~/logos
		}
	}
	abs, err := filepath.Abs(expandHome(dir))
	if err != nil {
		return "", false, recordFailed, err
	}
	// Before anything is recorded: the pointer is machine-wide and outlives the
	// run, and a file there left every host wired to a path that cannot hold a
	// vault, with the only failure printed ten lines above a table of ticks.
	// Usually a shell's doing — a tab-completion onto a neighbouring file, or an
	// empty $VAR that made the next word the path — not anybody's choice.
	if info, err := os.Stat(abs); err == nil && !info.IsDir() {
		return "", false, recordFailed, fmt.Errorf("%s is a file, not a directory — pass --vault <dir>; nothing was changed", abs)
	}
	if _, err := os.Stat(abs); os.IsNotExist(err) && flagStr(args, "--vault", "") == "" && !fromEnv && abs == vault.Pointer() {
		// Nobody asked for this directory in this run; it is the recorded vault,
		// and it is missing — usually an unmounted drive. Creating it makes an
		// empty vault at the mount path.
		return "", false, recordFailed, missingVaultError(abs)
	}
	if flagStr(args, "--vault", "") == "" && !fromEnv && looksLikeSourceTree(abs) {
		// Nobody chose this directory; it is the default, and it is a project.
		// `git clone …/logos` run in ~ lands exactly on ~/logos, and taking it
		// indexes the repository's markdown as notes and puts the user's memory
		// inside a tree `git clean` or a re-clone deletes.
		return "", false, recordFailed, fmt.Errorf("%s looks like a source checkout, not a vault — pass --vault <dir> to choose where the vault goes", abs)
	}
	if _, err := os.Stat(abs); os.IsNotExist(err) {
		created = true
		if !dryRun {
			// Private from the first mkdir. A vault created world-readable and
			// tightened later is a vault that was world-readable for however long
			// the user took to run `logos doctor`.
			if err := vault.MkdirPrivate(abs); err != nil {
				return "", false, recordFailed, fmt.Errorf("creating %s: %w", abs, err)
			}
		}
	}
	// A dry run reports the outcome the real run would reach, which under
	// LOGOS_VAULT is "not recorded" — the one command whose whole job is
	// previewing was promising the opposite of what followed.
	// doctor fails a recorded vault that lives under a temp root, because it
	// will be empty or gone. By then the pointer has already moved; setup is
	// the one place the check can stop it, so a temporary directory is used
	// for this run and recorded only when someone says so.
	//
	// --yes is not the answer to this one. It means "do not ask me questions",
	// and a script that passed it was also silently repointing the whole machine
	// at a scratch directory — the pointer is one file, and that is how it moved
	// without anyone deciding to move it. Waiving the guard needs its own flag.
	temp := !fromEnv && health.UnderTempDir(abs) && !hasFlag(args, "--record-temp")
	yes := hasFlag(args, "--yes") || hasFlag(args, "-y")
	if dryRun {
		if fromEnv {
			return abs, created, recordSkipEnv, nil
		}
		if temp {
			return abs, created, recordSkipTemp, nil
		}
		if move, _, _ := movingLoadedVault(args, abs); move {
			return abs, created, recordSkipMove, nil
		}
		return abs, created, recordedHere, nil
	}
	if fromEnv {
		return abs, created, recordSkipEnv, nil
	}
	if temp {
		fmt.Printf("             %s is a temporary directory — it will be empty or gone\n", abs)
		// No terminal to ask, so the safe answer is taken and named: a run that
		// silently did the dangerous thing is the bug being fixed here.
		if yes {
			fmt.Println("             not recording it — pass --record-temp to record it anyway")
			return abs, created, recordSkipTemp, nil
		}
		if !confirmNo("             record it as this machine's vault anyway?") {
			return abs, created, recordSkipTemp, nil
		}
	}
	// Moving a vault that holds work is the one setup decision worth its own
	// answer. The pointer is one file and `--vault B` rewrote it whether or not
	// A held every checkpoint this machine has taken — announced afterwards, in
	// the same receipt line as everything else. --yes does not answer this one
	// either, for the reason above it.
	if move, from, holds := movingLoadedVault(args, abs); move {
		fmt.Printf("             this machine's vault is %s, and it holds %s\n", from, holds)
		if yes {
			fmt.Println("             not moving it — pass --move-vault to move it anyway")
			return abs, created, recordSkipMove, nil
		}
		if !confirmNo(fmt.Sprintf("             make %s this machine's vault instead?", abs)) {
			return abs, created, recordSkipMove, nil
		}
	}
	// Write the choice down where a process with no shell can read it. A host
	// launched from Finder, such as Claude Desktop, inherits no LOGOS_VAULT, so
	// without this the server it starts can only find a vault at the default.
	if err := vault.Record(abs); err != nil {
		fmt.Printf("             could not record this vault for hosts started without LOGOS_VAULT: %v\n", err)
		return abs, created, recordFailed, nil
	}
	return abs, created, recordedHere, nil
}

// movingLoadedVault reports whether this run would repoint the machine away
// from a recorded vault that has checkpoints in it, names that vault, and says
// what is in it. An empty vault, or the one already recorded, is not a decision
// anybody needs to defend.
//
// The description is the part that makes the question answerable (#90). "It
// holds work" is true of a vault with one checkpoint and of a vault with a
// year of them, and the two deserve opposite answers — so the count is said
// before the prompt, not discovered afterwards by a resume that finds nothing.
func movingLoadedVault(args []string, abs string) (bool, string, string) {
	if hasFlag(args, "--move-vault") {
		return false, "", ""
	}
	prev := vault.Recorded()
	if prev == "" || filepath.Clean(prev) == abs {
		return false, "", ""
	}
	projects, err := session.Projects(prev)
	if err != nil {
		return false, "", ""
	}
	// A directory under sessions/ is not by itself work worth defending: it
	// also exists for a project that has only working notes. Asking about one
	// produced a warning whose own sentence said there was nothing to lose.
	holding, checkpoints := vaultHolding(prev, projects)
	if checkpoints == 0 {
		return false, "", ""
	}
	return true, prev, holding
}

// vaultHolding counts what would be left behind, in the terms the user names it
// in: checkpoints, and the projects they are filed under. A project directory
// that cannot be read counts as nothing rather than failing the move — this
// sentence exists to inform a decision, and refusing to describe the vault is a
// worse answer than describing the part of it that is readable.
func vaultHolding(prev string, projects []string) (string, int) {
	checkpoints, held := 0, 0
	for _, p := range projects {
		before := checkpoints
		checkpoints += checkpointsUnder(filepath.Join(prev, session.CheckpointDir, p))
		// Counted the same way as the checkpoints, for the same reason: a
		// project the user would be leaving nothing of is not one of the
		// projects this sentence is warning them about.
		if checkpoints > before {
			held++
		}
	}
	return fmt.Sprintf("%d %s across %d %s", checkpoints, plural(checkpoints, "checkpoint"), held, plural(held, "project")), checkpoints
}

// checkpointsUnder counts a project's checkpoints, including the ones a
// worktree keeps in its own subdirectory.
//
// One level down, not a full walk. A worktree scope is spelled
// "project/worktree" and session.Projects returns only the top level, so a
// vault whose work is all on branches counted zero and the "this vault holds
// work" prompt never appeared — setup repointed the machine away from it in
// silence. Two levels is the whole of the layout; recursing further would only
// find whatever else a user has put in their own directory.
func checkpointsUnder(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		// A project directory that cannot be read counts as nothing rather
		// than failing the move: this sentence exists to inform a decision,
		// and refusing to describe the vault is worse than describing the
		// part of it that is readable.
		return 0
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() {
			n += worktreeCheckpoints(filepath.Join(dir, e.Name()))
			continue
		}
		// The same predicate session.Read and doctor count with: a session
		// directory also holds the project's working notes, and calling those
		// a checkpoint overstates what the vault holds.
		if session.IsCheckpointFile(e.Name()) {
			n++
		}
	}
	return n
}

// checkpointsInVault counts every checkpoint under a vault's sessions
// directory: loose files at its top, and each project's with its worktrees.
// checkpointsUnder on the sessions directory itself stops a level short, at
// sessions/<project>/*.md, and missed a vault whose only work was on a branch.
func checkpointsInVault(sessions string) int {
	n := worktreeCheckpoints(sessions)
	entries, _ := os.ReadDir(sessions)
	for _, e := range entries {
		if e.IsDir() {
			n += checkpointsUnder(filepath.Join(sessions, e.Name()))
		}
	}
	return n
}

// worktreeCheckpoints counts the checkpoint files directly inside one
// worktree's directory, and does not descend again.
func worktreeCheckpoints(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() && session.IsCheckpointFile(e.Name()) {
			n++
		}
	}
	return n
}

// indexVault runs the first index so the vault is queryable immediately.
func indexVault(dir string) error {
	// The same guard `logos index` runs, and the one that matters most here:
	// setup is the path every user takes on day one, and `git init && git add
	// -A` in a vault without it commits index.db and #88's activity log — every
	// command and file path a host reported.
	if wrote, err := vault.EnsureGitignore(dir); err != nil {
		fmt.Printf("  index      could not write .gitignore: %v\n", err)
	} else if wrote {
		fmt.Println("  index      .gitignore now keeps .logos/ and activity/ out of git")
	}

	// Returned, not printed and dropped: the caller goes on to wire every host
	// to this vault, and a vault it could not index is not one to wire them to.
	ix, err := index.Open(dir)
	if err != nil {
		return err
	}
	defer ix.Close()

	rep, err := ix.Sync()
	if err != nil {
		return err
	}
	// provider.Discover rather than findProvider: the latter prints a banner of
	// its own, which would interrupt this report mid-table.
	embedModel := env("LOGOS_EMBED", defaultEmbedModel)
	if found := provider.Discover(); len(found) > 0 {
		// Said before it starts: a large vault takes minutes to embed, and
		// silence for that long reads as a hang with the hosts prompt stuck
		// behind it.
		var pending int
		ix.DB.QueryRow(`SELECT COUNT(*) FROM notes n LEFT JOIN embeddings e ON e.slug = n.slug WHERE e.slug IS NULL`).Scan(&pending)
		if pending > 0 {
			fmt.Printf("  index      embedding %d %s with %s — search already works without it…\n", pending, plural(pending, "note"), embedModel)
		}
		if _, err := ix.EmbedPending(found[0].Provider, embedModel, 32); err != nil {
			fmt.Printf("  index      embedding failed: %v — search is lexical until `logos index` succeeds\n", err)
		}
		ix.SyncMemories(found[0].Provider, embedModel)
	}
	notes, _ := ix.NoteCount()
	edges, _ := ix.EdgeCount()
	fmt.Printf("  index      %d notes, %d edges", notes, edges)
	if rep.Skipped > 0 {
		fmt.Printf(" (%d skipped)", rep.Skipped)
	}
	fmt.Println()
	return nil
}
