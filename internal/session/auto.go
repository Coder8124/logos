package session

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/Coder8124/logos/internal/gitstate"
	"github.com/Coder8124/logos/internal/vault"
)

// AutoLabel is what an auto checkpoint says about itself, in the file and
// wherever it is shown.
const AutoLabel = "auto — not written by an agent, unverified"

// WriteAuto writes a checkpoint for a session that ended without one, from what
// the host recorded it doing. The caller fills Task, Files and Commands; this
// marks it Auto and leaves every field that would be a claim empty.
//
// Only the file is written. Commit closes the project's open sessions and folds
// every open session's notes in, which is right for an agent vouching for its
// own work and wrong for a record nobody reviewed: another agent still working
// in the project would have its notes swept into an unverified checkpoint and
// its session closed under it. Notes left open stay listed as uncommitted
// beneath this checkpoint instead.
func WriteAuto(vaultDir string, c Checkpoint) (Checkpoint, error) {
	if safeScope(c.Project) == "" {
		return Checkpoint{}, fmt.Errorf("an auto checkpoint needs a project")
	}
	c.Project = safeScope(c.Project)
	if c.Agent == "" {
		c.Agent = "agent"
	}
	c.Auto = true
	c.Decisions, c.Failed, c.Verified, c.Blockers, c.Questions, c.Next = nil, nil, nil, nil, nil, ""
	// Kept when the caller set one: an auto checkpoint can be built from the
	// activity log or from the host's own transcript, and which it was is the
	// one thing a reader needs to weigh a record nobody reviewed.
	if c.State == "" {
		c.State = "Built from the activity log when the session ended without a checkpoint."
	}
	c.TS = time.Now().Unix()
	if c.Git.Empty() {
		c.Git = gitstate.Read(workingDir())
	}

	prev, _ := Latest(vaultDir, c.Project)
	var follows string
	if prev != nil {
		follows = prev.Session
	}
	id, path, err := claimCheckpoint(vaultDir, c.Project, c.Agent, idFor(c.Agent, time.Unix(c.TS, 0)))
	if err != nil {
		return Checkpoint{}, err
	}
	c.Session = id
	c.Slug = filepath.ToSlash(filepath.Join(CheckpointDir, c.Project, id))
	if err := vault.WriteAtomic(path, []byte(c.Markdown(follows))); err != nil {
		return Checkpoint{}, err
	}
	return c, nil
}
