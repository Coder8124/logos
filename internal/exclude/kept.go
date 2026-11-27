package exclude

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Coder8124/logos/internal/scope"
	"github.com/Coder8124/logos/internal/session"
)

// Kept is what Logos already holds from before an exclusion was made.
// Excluding stops new reads; it does not reach back, because deleting a
// person's record is theirs to ask for by name.
type Kept struct{ Transcripts, Checkpoints int }

// projectRoot is the vault folder a project's sessions are filed under, and
// "" for a folder that names no project.
func projectRoot(vault, dir string) string {
	proj, _, _ := strings.Cut(session.SafeScope(scope.Name(dir)), "/")
	if proj == "" {
		return ""
	}
	return filepath.Join(vault, session.CheckpointDir, proj)
}

// walkKept visits every archived transcript and checkpoint under a project's
// folder, worktree scopes included, and nothing else. Counting and erasing
// both go through it, so what is reported as kept and what is removed can
// never disagree about which files belong to the project. A visit that fails
// stops the walk and its error is returned.
func walkKept(root string, visit func(path string, transcript bool) error) error {
	if root == "" {
		return nil
	}
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		switch {
		case session.IsTranscriptFile(d.Name()):
			return visit(p, true)
		case session.IsCheckpointFile(d.Name()):
			return visit(p, false)
		}
		return nil
	})
}

// KeptForProject counts the archived transcripts and checkpoints filed under
// the project a folder belongs to, worktree scopes included.
func KeptForProject(vault, dir string) (Kept, string) {
	root := projectRoot(vault, dir)
	var k Kept
	_ = walkKept(root, func(_ string, transcript bool) error {
		if transcript {
			k.Transcripts++
		} else {
			k.Checkpoints++
		}
		return nil
	})
	return k, root
}

// EraseProject removes what KeptForProject counts and nothing more: working
// notes, the review queue and any file it does not recognise stay. A folder
// is removed only once it is empty. The caller syncs the index afterwards
// (vault first, index second). A removal that fails returns the counts so far
// with the error, never a success-shaped result.
func EraseProject(vault, dir string) (Kept, string, error) {
	root := projectRoot(vault, dir)
	var k Kept
	parents := map[string]bool{}
	err := walkKept(root, func(p string, transcript bool) error {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("could not remove %s: %w", p, err)
		}
		if transcript {
			k.Transcripts++
		} else {
			k.Checkpoints++
		}
		parents[filepath.Dir(p)] = true
		return nil
	})
	// Deepest first, and os.Remove refuses a folder that still holds anything,
	// so a folder with a file we do not own survives.
	dirs := make([]string, 0, len(parents))
	for d := range parents {
		dirs = append(dirs, d)
	}
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) > len(dirs[j]) })
	for _, d := range dirs {
		for strings.HasPrefix(d, root) {
			if os.Remove(d) != nil || d == root {
				break
			}
			d = filepath.Dir(d)
		}
	}
	return k, root, err
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
