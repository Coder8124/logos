package session

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/Coder8124/brain/internal/vault"
)

// Working notes, made durable.
//
// note_progress is sold on exactly one promise: "cheap, and it survives your
// context running out, which a plan held only in your head does not." It did
// not. A note lived in session_notes in .brain/index.db and nowhere else, so
// the operation the documentation calls safe — delete the cache and reindex —
// destroyed every uncommitted note in the vault without a word. `brain sessions`
// afterwards reported "no checkpoints yet", which is what having lost them
// looks like and also what never having written them looks like.
//
// This is the same repair memories got, for the same reason and in the same
// shape: the vault is the truth and the index is a cache of it. Notes are
// written through to sessions/<scope>/uncommitted.md as they are made, read
// back by `brain index`, and removed when a checkpoint folds them into a
// permanent record.
//
// The file is deliberately ordinary markdown in the ordinary place. Someone who
// opens their vault mid-task should find the loose ends where the finished work
// already is, not in a format that needs this program to read.

// NotesFile is the working-notes file inside a scope's session directory.
const NotesFile = "uncommitted.md"

// The vault each index belongs to, keyed by its database — the same mechanism
// internal/memory uses, and for the same reason: it threads a path to eight
// call sites without touching a single signature.
var (
	noteVaultMu sync.RWMutex
	noteVaults  = map[*sql.DB]string{}
)

// SetVault binds a database to the vault its notes belong in. An empty dir
// unbinds it, which is what a closing index does and what a test that only
// cares about the cache wants.
func SetVault(db *sql.DB, dir string) {
	noteVaultMu.Lock()
	defer noteVaultMu.Unlock()
	if dir == "" {
		delete(noteVaults, db)
		return
	}
	noteVaults[db] = dir
}

func noteVaultFor(db *sql.DB) string {
	noteVaultMu.RLock()
	defer noteVaultMu.RUnlock()
	return noteVaults[db]
}

// notesPath is where one scope's working notes live. Worktrees are a sub-scope
// spelled "project/worktree", so this mirrors the checkpoint layout exactly and
// a worktree's loose ends sit beside its own checkpoints.
func notesPath(vaultDir, scope string) string {
	return filepath.Join(vaultDir, CheckpointDir, filepath.FromSlash(safeScope(scope)), NotesFile)
}

// flushNotes rewrites one scope's working-notes file from the index.
//
// Whole-file, like the memory export and for the same reason: a note is not
// only appended, it is also swept away by a checkpoint, and reconstructing that
// from a log is how the cache became authoritative in the first place. There are
// never many of them.
//
// Errors are returned. Silently failing here would recreate the exact bug this
// file exists to close — a note the caller was told was saved, living only in a
// cache the user has been told to feel free to delete.
func flushNotes(db *sql.DB, scope string) error {
	dir := noteVaultFor(db)
	if dir == "" || safeScope(scope) == "" {
		return nil
	}
	notes, err := Uncommitted(db, scope)
	if err != nil {
		return err
	}
	path := notesPath(dir, scope)
	if len(notes) == 0 {
		// Nothing outstanding. Remove the file rather than leaving an empty one:
		// a lingering "uncommitted" heading claims work is pending that is not.
		return removeNotes(dir, scope)
	}
	if err := vault.MkdirPrivate(filepath.Dir(path)); err != nil {
		return err
	}
	return vault.WriteAtomic(path, []byte(renderNotes(scope, notes)))
}

// removeNotes deletes a scope's working-notes file. Absence is success — the
// common case is a project that has been checkpointed twice in a row.
func removeNotes(vaultDir, scope string) error {
	if vaultDir == "" || safeScope(scope) == "" {
		return nil
	}
	if err := os.Remove(notesPath(vaultDir, scope)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func renderNotes(scope string, notes []Note) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# uncommitted — %s\n\n", scope)
	b.WriteString("Working notes from sessions that have not been checkpointed yet.\n")
	b.WriteString("`brain checkpoint " + scope + "` folds these into a permanent record and\n")
	b.WriteString("empties this file. Until then they live here, so a rebuilt index does not\n")
	b.WriteString("lose them.\n\n")
	for _, n := range notes {
		// oneLine, because a note holding a newline would render as a bullet the
		// parser cannot read back — a lost note wearing the costume of a saved one.
		fmt.Fprintf(&b, "- %s <!-- ts=%d agent=%s -->\n", oneLine(n.Text), n.TS, safe(n.Agent))
	}
	return b.String()
}

var notePattern = regexp.MustCompile(`^-\s+(.*?)\s*<!--\s*ts=(\d+)\s+agent=(\S*)\s*-->\s*$`)

// ImportNotes restores working notes from the vault into an empty index.
//
// Called by `brain index`, which is the command whose whole promise is that the
// cache can be rebuilt from the vault. It adds only what is missing: a note
// already in the index is left alone, matched on its scope, text and timestamp,
// so running index twice does not double anything.
//
// It never deletes. A note present in the index and absent from the file is not
// evidence of a deletion — it is what a note written by a running agent since
// the last flush looks like, and the safe reading of an ambiguity here is the
// one that keeps the work.
//
// Returns how many notes it restored.
func ImportNotes(db *sql.DB, vaultDir string) (int, error) {
	// A rebuild is exactly the case where the tables do not exist yet — the user
	// deleted the database — so create them rather than reporting their absence
	// as a failure to restore.
	if err := Init(db); err != nil {
		return 0, err
	}
	root := filepath.Join(vaultDir, CheckpointDir)
	var restored int

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// One unreadable directory must not abort the rest of the rebuild.
			return nil //nolint:nilerr
		}
		if d.IsDir() || d.Name() != NotesFile {
			return nil
		}
		rel, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return nil
		}
		scope := filepath.ToSlash(rel)
		if scope == "." || scope == "" {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		n, err := importScopeNotes(db, scope, string(raw))
		if err != nil {
			return err
		}
		restored += n
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return restored, err
	}
	return restored, nil
}

func importScopeNotes(db *sql.DB, scope, raw string) (int, error) {
	existing, err := Uncommitted(db, scope)
	if err != nil {
		return 0, err
	}
	have := map[string]bool{}
	for _, n := range existing {
		have[n.Text+"\x00"+strconv.FormatInt(n.TS, 10)] = true
	}

	var restored int
	for _, line := range strings.Split(raw, "\n") {
		m := notePattern.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		text := strings.TrimSpace(m[1])
		ts, _ := strconv.ParseInt(m[2], 10, 64)
		if text == "" || have[text+"\x00"+m[2]] {
			continue
		}
		agent := m[3]
		if agent == "" {
			agent = "agent"
		}
		if _, err := addNoteNoFlush(db, scope, agent, text, ts); err != nil {
			return restored, err
		}
		have[text+"\x00"+m[2]] = true
		restored++
	}
	return restored, nil
}
