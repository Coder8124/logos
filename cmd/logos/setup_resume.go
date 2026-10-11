package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Coder8124/logos/internal/bootstrap"
	"github.com/Coder8124/logos/internal/contextpack"
	"github.com/Coder8124/logos/internal/gitstate"
	"github.com/Coder8124/logos/internal/index"
	"github.com/Coder8124/logos/internal/memory"
	"github.com/Coder8124/logos/internal/session"
)

// setupAgent is who the first checkpoint says wrote it. Not an agent's name, so
// nobody reading the resume takes setup's line for work an agent did.
const setupAgent = "logos-setup"

// resumeExcerptLines caps the handoff setup shows: all of setup's own
// checkpoint, and short enough that a real one still leaves setup ending on one
// screen.
const resumeExcerptLines = 20

// firstResume ends setup on proof instead of a list of hosts wired.
//
// A list of ✓s says config files were written. What a new user is deciding is
// whether their next agent will know anything, and the only way to show that
// is to do it: write a checkpoint for the repository they ran setup in, read it
// back the way the session-start hook will, and print what came out. That is
// also the one moment a new user will read the resume, rather than an agent.
//
// Outside a repository there is no project to show it for, and the closing
// steps above already say how to try it in one.
func firstResume(vaultDir string, wired []string, opts wireOpts) {
	cwd, err := os.Getwd()
	if err != nil {
		return
	}
	out, err := gitstate.SafeGit(cwd, 3*time.Second, "rev-parse", "--show-toplevel")
	if err != nil {
		return
	}
	root := strings.TrimSpace(out)
	project := projectFor(cwd)
	if project == "" {
		return
	}

	// openEvents is the vault the hosts were wired to: setupCmd pinned
	// LOGOS_VAULT to it for the run.
	ix, err := openEvents()
	if err != nil {
		fmt.Printf("\n  resume     could not open the vault to show it: %v\n", err)
		return
	}
	defer ix.Close()
	for _, init := range []func() error{
		func() error { return memory.Init(ix.DB) },
		func() error { return session.Init(ix.DB) },
	} {
		if err := init(); err != nil {
			fmt.Printf("\n  resume     could not open the vault to show it: %v\n", err)
			return
		}
	}

	fmt.Printf("\n  project    %s (%s)\n", project, root)
	held := checkpointsUnder(filepath.Join(vaultDir, session.CheckpointDir, filepath.FromSlash(session.SafeScope(project))))
	notes, err := session.Uncommitted(ix.DB, project)
	if err != nil {
		// Without the notes setup cannot tell whether an agent left work here,
		// and a checkpoint saying none was recorded may be false.
		fmt.Printf("  resume     could not read the working notes for %s: %v\n", project, err)
		return
	}
	switch {
	case held > 0:
		// A setup line on top of real work would bury the handoff it is
		// meant to demonstrate.
		fmt.Printf("  checkpoint %d already here for %s — none written by setup\n", held, project)
	case len(notes) > 0:
		// An agent recorded notes here and never checkpointed. Setup's
		// checkpoint would fold them in under a State saying no work has been
		// recorded, so they are left for the next real checkpoint to claim;
		// resume shows them meanwhile.
		fmt.Printf("  notes      %d working %s already here for %s, not yet checkpointed — none written by setup\n",
			len(notes), plural(len(notes), "note"), project)
	default:
		// Only into a vault with nothing in it: memories derived from history
		// are recalled as if someone had asserted them, so they are offered
		// where there is nothing else yet, and never on top of real work.
		if n, err := memory.Count(ix.DB); err == nil && n == 0 && checkpointsInVault(filepath.Join(vaultDir, session.CheckpointDir)) == 0 {
			offerBootstrap(root, project, opts.yes)
		}
		if err := writeSetupCheckpoint(ix, project, wired); err != nil {
			// The hosts are wired and setup succeeded; only the demonstration
			// failed, and it says so rather than showing an empty resume.
			fmt.Printf("  checkpoint could not be written: %v\n", err)
			return
		}
		fmt.Println("  checkpoint written by setup — it says where Logos started recording this project")
	}

	pack, err := contextpack.Build(ix, nil, "", contextpack.Request{
		Task: "resume work on " + project, Hint: project, Dir: dirFor(project),
	})
	if err != nil {
		fmt.Printf("  resume     failed: %v\n", err)
		return
	}
	if pack.Empty() {
		fmt.Printf("  resume     came back empty for %s, though a checkpoint is there — `logos doctor` checks the vault\n", project)
		return
	}
	fmt.Printf("\n  $ logos resume %s\n\n", project)
	lines := strings.Split(strings.TrimRight(pack.Render(), "\n"), "\n")
	shown := handoffExcerpt(lines)
	for _, l := range shown {
		fmt.Println(strings.TrimRight("    "+l, " "))
	}
	if rest := len(lines) - len(shown); rest > 0 {
		fmt.Printf("\n    … %d more lines — `logos resume %s` prints them all\n", rest, project)
	}
	fmt.Println("\n  This is what your agent will see at the start of its next session")
	fmt.Println("  in this repository.")
}

// handoffExcerpt is the "Where we left off" section of a rendered resume, up
// to resumeExcerptLines. The pack opens with a title and notes on framing and
// filtering; an excerpt taken from the top spent its lines on those and was
// cut off before it said what the last agent was doing — the one part setup
// is there to show. Notes no checkpoint has claimed are that part when there
// is no checkpoint, so their heading starts the excerpt too. Without either,
// it is the top of the pack.
func handoffExcerpt(lines []string) []string {
	start := 0
	for i, l := range lines {
		if strings.HasPrefix(l, "## Where we left off") || strings.HasPrefix(l, "## Recorded since, not yet checkpointed") {
			start = i
			break
		}
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "## ") {
			end = i
			break
		}
	}
	end = min(end, start+resumeExcerptLines)
	for end > start && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return lines[start:end]
}

// writeSetupCheckpoint files the first checkpoint for project, labelled as
// setup's own. Verified says connected and no more: when the Logos plugin
// carries Claude Code, setup ran no handshake to claim.
func writeSetupCheckpoint(ix *index.Index, project string, wired []string) error {
	hosts := wired[0]
	if len(wired) > 1 {
		hosts = strings.Join(wired[:len(wired)-1], ", ") + " and " + wired[len(wired)-1]
	}
	c := &session.Checkpoint{
		Project: project,
		Agent:   setupAgent,
		Task:    "Connect Logos to the agents on this machine",
		State: "Written by `logos setup`, not by an agent: no work on this project has been " +
			"recorded yet. This marks where Logos started keeping it.",
		Verified: []string{fmt.Sprintf("%s connected to this vault", hosts)},
		Commands: []string{"logos setup"},
		Next: "Work as usual. Before stopping, checkpoint — what was decided, what was ruled out " +
			"and why, what was verified — so the next agent in this repository starts from there " +
			"instead of from this line.",
	}
	return session.Commit(ix.DB, ix.Vault, c)
}

// offerBootstrap offers to seed an empty vault from the repository's history.
// --yes does not take it: these are claims about the user's own project,
// recalled later as if they had made them, so they read them first.
func offerBootstrap(root, project string, yes bool) {
	found, unread := bootstrap.FromGitHistory(root, 12)
	if len(found) == 0 {
		return
	}
	// What follows is drawn from the history, so a part git would not read is
	// named first, as `logos bootstrap` names it (#204).
	for _, what := range unread {
		fmt.Printf("  history    git refused to read %s here, so nothing below is drawn from it\n", what)
	}
	if yes {
		fmt.Printf("  history    %d %s could be seeded from this repository's git history — `logos bootstrap` shows them first\n",
			len(found), map[bool]string{true: "memory", false: "memories"}[len(found) == 1])
		return
	}
	fmt.Printf("\n  From this repository's git history, as memories for %s:\n\n", project)
	printSeeds(found)
	if !confirmBootstrap(len(found)) {
		fmt.Println("  Nothing written. `logos bootstrap` offers them again.")
		return
	}
	if err := storeSeeds(found, project); err != nil {
		fmt.Printf("  bootstrap  failed: %v\n", err)
	}
}
