package session

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

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

// Commit writes the checkpoint to the vault and closes the session. This is the
// commit in "working tree, then commit": until it is called, everything the
// agent recorded lives only in the rebuildable index and is not really saved.
//
// The working notes of the session are folded in as state when the caller left
// state blank, so an agent that only ever called note_progress still produces a
// useful checkpoint instead of an empty one.
func Commit(db *sql.DB, vaultDir string, c *Checkpoint) error {
	if strings.TrimSpace(c.Project) == "" {
		return fmt.Errorf("a checkpoint needs a project")
	}
	c.Project = safe(c.Project)
	if strings.TrimSpace(c.Agent) == "" {
		c.Agent = "agent"
	}
	if c.TS == 0 {
		c.TS = time.Now().Unix()
	}

	// Bind to the project's open session so working notes and the checkpoint
	// describe the same stretch of work.
	s, err := Current(db, c.Project, c.Agent)
	if err != nil {
		return err
	}
	c.Session = s.ID
	if c.Task == "" {
		c.Task = s.Task
	}

	notes, err := Notes(db, s.ID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(c.State) == "" && len(notes) > 0 {
		lines := make([]string, 0, len(notes))
		for _, n := range notes {
			lines = append(lines, "- "+n.Text)
		}
		c.State = "Recorded during the session:\n" + strings.Join(lines, "\n")
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
	return close_(db, s.ID, c.Slug)
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
