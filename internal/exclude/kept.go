package exclude

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/Coder8124/logos/internal/scope"
	"github.com/Coder8124/logos/internal/session"
)

// Kept is what Logos already holds from before an exclusion was made.
// Excluding stops new reads; it does not reach back, because deleting a
// person's record is theirs to ask for by name.
type Kept struct{ Transcripts, Checkpoints int }

// KeptForProject counts the archived transcripts and checkpoints filed under
// the project a folder belongs to, worktree scopes included.
func KeptForProject(vault, dir string) (Kept, string) {
	proj, _, _ := strings.Cut(session.SafeScope(scope.Name(dir)), "/")
	var k Kept
	if proj == "" {
		return k, ""
	}
	root := filepath.Join(vault, session.CheckpointDir, proj)
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		switch {
		case session.IsTranscriptFile(d.Name()):
			k.Transcripts++
		case session.IsCheckpointFile(d.Name()):
			k.Checkpoints++
		}
		return nil
	})
	return k, root
}

// Ambiguous is the error for a short session id that fits more than one kept
// conversation. Nothing is counted or erased on a guess.
type Ambiguous struct {
	ID      string
	Matches []string
}

func (e *Ambiguous) Error() string {
	return fmt.Sprintf("%q fits %d kept conversations (%s): give more of the id", e.ID, len(e.Matches), strings.Join(e.Matches, ", "))
}

// Resolve is the full session id a typed one names among what the vault keeps:
// the id itself when it is one exactly, else the one kept id it is a prefix of
// (at least minPrefix characters, as an exclusion matches). More than one is
// an Ambiguous error; none is the id as typed, so counts come out zero rather
// than the command failing for a chat that was simply never archived.
func Resolve(vault, id string) (string, error) {
	id = strings.TrimSpace(id)
	var hits []string
	for _, k := range session.KeptSessionIDs(vault) {
		if k == id {
			return k, nil
		}
		if SessionMatches(id, k) {
			hits = append(hits, k)
		}
	}
	switch len(hits) {
	case 0:
		return id, nil
	case 1:
		return hits[0], nil
	}
	return "", &Ambiguous{ID: id, Matches: hits}
}

// KeptForSession counts what is kept for one host session id (or a prefix of
// one, see Resolve). A chat has one archived transcript per conversation
// folder; the lookup names the first.
func KeptForSession(vault, id string) (Kept, error) {
	var k Kept
	full, err := Resolve(vault, id)
	if err != nil {
		return k, err
	}
	if session.FindTranscript(vault, full) != "" {
		k.Transcripts = 1
	}
	cps, _ := session.ByHostSession(vault, full)
	k.Checkpoints = len(cps)
	return k, nil
}

// Erase removes one chat's archived transcript and checkpoints from the vault
// and returns how many of each. id may be a prefix (see Resolve); one that
// fits two conversations is refused before anything is removed. The caller
// then syncs the index: the vault is written first so a crash between the two
// leaves a stale cache, which the next index fixes, never an index entry for a
// file that is gone.
//
// A removal that fails is returned with what had already gone, never swallowed:
// a half-erased conversation reported as erased is the worst answer.
func Erase(vault, id string) (Kept, error) {
	var k Kept
	id, err := Resolve(vault, id)
	if err != nil {
		return k, err
	}
	for i := 0; i < 16; i++ {
		p := session.FindTranscript(vault, id)
		if p == "" {
			break
		}
		if err := os.Remove(p); err != nil {
			return k, fmt.Errorf("could not remove the archived transcript %s: %w", p, err)
		}
		k.Transcripts++
		// An empty conversation folder is the shell of what was erased.
		_ = os.Remove(filepath.Dir(p))
	}
	cps, err := session.ByHostSession(vault, id)
	if err != nil {
		return k, fmt.Errorf("could not list the checkpoints for %s: %w", id, err)
	}
	for _, c := range cps {
		if c.Slug == "" {
			continue
		}
		f := filepath.Join(vault, filepath.FromSlash(c.Slug)+".md")
		if err := os.Remove(f); err != nil && !os.IsNotExist(err) {
			return k, fmt.Errorf("could not remove the checkpoint %s: %w", f, err)
		}
		k.Checkpoints++
		_ = os.Remove(filepath.Dir(f))
	}
	return k, nil
}
