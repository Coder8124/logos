package session

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/Coder8124/logos/internal/gitstate"
	"github.com/Coder8124/logos/internal/vault"
)

// AutoLabel is what an auto checkpoint says about itself, in the file and
// wherever it is shown.
const AutoLabel = "auto — not written by an agent, unverified"

// ActivityLogState is what an auto record built from the activity log said
// before it named the session it came from. The sweep still matches it, by
// time, so records written before that are not recorded a second time.
const ActivityLogState = "Built from the activity log when the session ended without a checkpoint."

// ActivityLogStateFor is the provenance of an auto record built from the
// activity log of host session id. The id in parentheses is how the sweep
// knows that session's transcript was recorded, in the same shape the
// transcript-built record uses.
func ActivityLogStateFor(id string) string {
	return fmt.Sprintf("Built from the activity log (%s) when the session ended without a checkpoint.", id)
}

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
		c.State = ActivityLogState
	}
	// A caller's own time means a session that ended earlier, recorded late.
	// The repository now is not the repository that session left, so no git
	// state is read for it; and what it follows is the checkpoint before it,
	// not the newest one.
	past := c.TS != 0
	if !past {
		c.TS = time.Now().Unix()
	}
	if c.Git.Empty() && !past {
		c.Git = gitstate.Read(workingDir())
	}

	var follows string
	if past {
		history, err := History(vaultDir, c.Project, 0)
		if err != nil {
			return Checkpoint{}, err
		}
		for _, h := range history {
			if h.TS < c.TS {
				follows = h.Session
				break
			}
		}
	} else if prev, _ := Latest(vaultDir, c.Project); prev != nil {
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

// GrowAuto rewrites the auto record old with what its session has done since,
// in the same file under the same id, and returns it as written.
//
// A session can outlive its record: a window left idle long enough to be swept
// and then used again, or a host resumed on the transcript it had already
// written. A second record would list the first one's work again beside it,
// and ignoring the rest would lose it, which is the thing the record is for.
// The file stays where it is so whatever already links to it still finds it.
//
// The file is read again first, and anything no longer marked auto is left
// alone: a person may have made it their own since, and a mechanical list must
// never be written over what somebody reviewed.
func GrowAuto(vaultDir string, old, c Checkpoint) (Checkpoint, error) {
	if old.Slug == "" {
		return Checkpoint{}, fmt.Errorf("an auto record to grow needs the file it was read from")
	}
	path := filepath.Join(vaultDir, filepath.FromSlash(old.Slug)+".md")
	raw, err := os.ReadFile(path)
	if err != nil {
		return Checkpoint{}, err
	}
	if !ParseCheckpoint(string(raw)).Auto {
		return Checkpoint{}, fmt.Errorf("%s is no longer an auto record, so it was not rewritten", old.Slug)
	}
	c.Project, c.Session, c.Slug, c.Git = old.Project, old.Session, old.Slug, old.Git
	if c.Agent == "" {
		c.Agent = old.Agent
	}
	c.Auto = true
	c.Decisions, c.Failed, c.Verified, c.Blockers, c.Questions, c.Next = nil, nil, nil, nil, nil, ""
	if c.TS == 0 {
		c.TS = time.Now().Unix()
	}
	var follows string
	if m := followsLink.FindStringSubmatch(string(raw)); m != nil {
		follows = m[1]
	}
	if err := vault.WriteAtomic(path, []byte(c.Markdown(follows))); err != nil {
		return Checkpoint{}, err
	}
	return c, nil
}

// followsLink is how Markdown writes the chain backwards; a rewrite keeps it.
var followsLink = regexp.MustCompile(`pred: follows, obj: "\[\[([^\]]+)\]\]"`)
