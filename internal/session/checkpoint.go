package session

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pragun/brain/internal/gitstate"
	"github.com/pragun/brain/internal/vault"
)

// A Checkpoint is where an agent stopped, written down well enough that a
// different agent can start.
//
// Failed is the field that earns its keep. Anyone can restate the goal; the
// expensive knowledge is the three approaches already ruled out, and it is
// exactly what gets lost when a session ends. A checkpoint without it saves the
// next agent a paragraph of reading and costs it an afternoon of rediscovery.
type Checkpoint struct {
	Session   string
	Project   string
	Agent     string
	Task      string
	State     string
	Decisions []string
	Failed    []string
	Questions []string
	Files     []string
	Next      string
	// Git is the repository as it stood, read rather than reported. The half of
	// a checkpoint an agent cannot forget to fill in — see the comment in
	// Commit, and package gitstate.
	Git gitstate.State
	// HandoffTo names who this was left for. Empty for a plain checkpoint —
	// the difference is intent, not mechanism.
	HandoffTo string
	Slug      string // vault slug, set once written
	TS        int64
}

// CheckpointDir is where checkpoints live inside the vault. A visible folder,
// not a dotfile: these are notes you are meant to be able to open, read, and
// put under version control alongside everything else.
const CheckpointDir = "sessions"

func (c Checkpoint) Empty() bool {
	return strings.TrimSpace(c.Task) == "" && strings.TrimSpace(c.State) == "" &&
		strings.TrimSpace(c.Next) == "" && len(c.Decisions) == 0 &&
		len(c.Failed) == 0 && len(c.Questions) == 0
}

// Commit writes the checkpoint to the vault and closes the project's open
// sessions. This is the commit in "working tree, then commit": until it is
// called, everything the agent recorded lives only in the rebuildable index and
// is not really saved.
//
// The project's uncommitted working notes are folded in — including any left by
// an agent that died mid-task — so an agent that only ever called note_progress
// still produces a useful checkpoint instead of an empty one.
func Commit(db *sql.DB, vaultDir string, c *Checkpoint) error {
	if strings.TrimSpace(c.Project) == "" {
		return fmt.Errorf("a checkpoint needs a project")
	}
	// Distinguish "you gave no project" from "that project name cannot be a
	// filename" — reporting the second as the first blames the caller for
	// omitting what they supplied.
	if safe(c.Project) == "" {
		return fmt.Errorf(
			"project name %q has no letters or digits to make a filename from", c.Project)
	}
	c.Project = safe(c.Project)
	if strings.TrimSpace(c.Agent) == "" {
		c.Agent = "agent"
	}
	if c.TS == 0 {
		c.TS = time.Now().Unix()
	}

	// Bind to this agent's session. The id becomes the checkpoint's filename, so
	// it has to name whoever is actually writing it.
	s, err := Current(db, c.Project, c.Agent)
	if err != nil {
		return err
	}
	c.Session = s.ID
	if c.Task == "" {
		c.Task = s.Task
	}

	// Observe the repository, rather than waiting to be told about it.
	//
	// Everything above this line is what the agent chose to say. This is what
	// was actually true — branch, commit, and what was still uncommitted — read
	// straight from git. An agent that forgets to describe its state cannot
	// forget this, which is the whole point: the model is no longer the only
	// path by which a checkpoint becomes useful.
	//
	// Only filled when the caller did not set it, so a replayed or imported
	// checkpoint keeps the state it was recorded with instead of being
	// overwritten by whatever the working tree happens to look like now.
	if c.Git.Empty() {
		c.Git = gitstate.Read(workingDir())
	}

	// Fold the project's uncommitted working notes into the record. They are
	// appended, not substituted: a checkpoint closes the sessions, so anything
	// left only in session_notes becomes unreachable — dropping them whenever
	// the agent also wrote a state paragraph would silently lose everything
	// note_progress collected.
	//
	// Every open session's notes, not only this agent's. When work passes from
	// an agent that died mid-task, its findings are exactly what the next agent
	// is building on, and they have to end up in the durable record or they are
	// lost the moment anyone checkpoints.
	notes, err := Uncommitted(db, c.Project)
	if err != nil {
		return err
	}
	if len(notes) > 0 {
		lines := make([]string, 0, len(notes))
		for _, n := range notes {
			// Attribute when more than one agent contributed: which of them
			// found a thing is part of the finding.
			if n.Agent != "" && n.Agent != c.Agent {
				lines = append(lines, "- ("+n.Agent+") "+n.Text)
			} else {
				lines = append(lines, "- "+n.Text)
			}
		}
		recorded := "Recorded during the session:\n" + strings.Join(lines, "\n")
		if strings.TrimSpace(c.State) == "" {
			c.State = recorded
		} else {
			c.State = strings.TrimSpace(c.State) + "\n\n" + recorded
		}
	}
	if c.Empty() {
		return fmt.Errorf("nothing to checkpoint — record some progress first")
	}

	prev, _ := Latest(vaultDir, c.Project)
	var follows string
	if prev != nil {
		follows = prev.Session
	}

	c.Slug = filepath.ToSlash(filepath.Join(CheckpointDir, c.Project, s.ID))
	path := filepath.Join(vaultDir, filepath.FromSlash(c.Slug)+".md")
	if err := vault.WriteAtomic(path, []byte(c.Markdown(follows))); err != nil {
		return err
	}
	return closeProject(db, c.Project, c.Slug)
}

// Latest returns the most recent checkpoint for a project, or nil if there is
// none.
//
// It reads the vault directory rather than a table on purpose. Checkpoints are
// the one thing here that must survive `rm -rf .brain` — if resume depended on
// the index, the markdown would be a souvenir rather than the record. Filenames
// begin with a sortable timestamp, so "most recent" is a sort, not a query.
func Latest(vaultDir, project string) (*Checkpoint, error) {
	all, err := History(vaultDir, project, 1)
	if err != nil || len(all) == 0 {
		return nil, err
	}
	return &all[0], nil
}

// History returns up to n checkpoints for a project, newest first.
func History(vaultDir, project string, n int) ([]Checkpoint, error) {
	dir := filepath.Join(vaultDir, CheckpointDir, safe(project))
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			names = append(names, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	if n > 0 && len(names) > n {
		names = names[:n]
	}

	out := make([]Checkpoint, 0, len(names))
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue // a checkpoint we cannot read must not hide the ones we can
		}
		c := ParseCheckpoint(string(raw))
		c.Slug = filepath.ToSlash(filepath.Join(CheckpointDir, safe(project), strings.TrimSuffix(name, ".md")))
		out = append(out, c)
	}
	return out, nil
}

// Projects lists the projects that have at least one checkpoint.
func Projects(vaultDir string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(vaultDir, CheckpointDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// workingDir is where the agent is standing. A coding agent's cwd is the repo it
// is working in — that is the assumption the whole product makes — and an MCP
// server inherits it from the host that launched it.
func workingDir() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	return dir
}
