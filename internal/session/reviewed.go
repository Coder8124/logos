package session

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Coder8124/logos/internal/vault"
)

// ReviewedFile records what `logos review --stale` decided about a ruling a
// checkpoint holds. A checkpoint is never edited — it is the record of what
// the agent said at the time — so a person's later "this no longer holds" or
// "still true at this commit" cannot be written into it, and it cannot live in
// the index either: deleting .logos would quietly un-retire every ruling and
// send the drift warnings back. It sits beside uncommitted.md, which
// IsCheckpointFile already tells apart from a checkpoint by its name.
const ReviewedFile = "reviewed.md"

// LedgerFile is the project's long-term history derived from its
// checkpoints (internal/ops/ledger.go). It sits beside reviewed.md for the
// same reason: IsCheckpointFile tells it apart by its name, so History never
// reads the ledger back as a checkpoint it was built from.
const LedgerFile = "ledger.md"

// LedgerType is the frontmatter type a ledger is written under. Derived files
// are told apart by it, not by their name: a sync client's conflict copy,
// "ledger (conflicted copy).md", is the same view under another name, and
// keying on "ledger.md" let it into the index and through a rename as if
// someone had written it.
const LedgerType = "ledger"

// IsDerivedFile is whether the vault file at rel (slash-separated, from the
// vault root), starting with head, is a view Logos rebuilds from checkpoints
// rather than a record: where a ledger is written (derivedPlace), and typed
// ledger by its frontmatter's own top-level key. A rename deletes what this
// says yes to, so both have to hold: a note of the user's at
// sessions/ledger.md, one under another name, or one nesting "type: ledger"
// under some other key is theirs whatever it says it is.
func IsDerivedFile(rel string, head []byte) bool {
	return derivedPlace(rel) && ledgerTyped(rel, head)
}

// DerivedFileAt is IsDerivedFile for the file at path inside vaultDir, read
// only as far as its frontmatter, and only once its place says it could be a
// ledger. A file it cannot read is not derived: the caller's own read reports
// the failure.
func DerivedFileAt(vaultDir, path string) bool {
	rel, err := filepath.Rel(vaultDir, path)
	if err != nil {
		return false
	}
	rel = filepath.ToSlash(rel)
	if !derivedPlace(rel) {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 512)
	n, _ := f.Read(head)
	return ledgerTyped(rel, head[:n])
}

// derivedPlace is where a ledger can be: inside a project's (or worktree's)
// checkpoint folder, named ledger.md or as a sync client names its copy of
// it. The one copy of the location rule, so the index and a rename cannot
// disagree about which files are Logos's to rebuild.
func derivedPlace(rel string) bool {
	parts := strings.Split(rel, "/")
	if len(parts) < 3 || parts[0] != CheckpointDir {
		return false
	}
	name := parts[len(parts)-1]
	return name == LedgerFile || ledgerCopy.MatchString(name)
}

// ledgerCopy is ledger.md as a sync client renames a second copy of it:
// Dropbox's "ledger (… conflicted copy …)", Google Drive's "ledger (1)",
// iCloud's "ledger 2", Syncthing's "ledger.sync-conflict-<date>-<time>-<id>",
// and a merge's "ledger-2". Nothing looser: "ledger (draft).md" or
// "ledger-notes.md" is a person's file, and a rename would delete it.
var ledgerCopy = regexp.MustCompile(`^ledger(?: \([^()]*conflict[^()]*\)| \(\d+\)| \d+|-\d+|\.sync-conflict-\d{8}-\d{6}-[A-Za-z0-9]+)\.md$`)

// ledgerTyped is whether head opens with frontmatter whose top-level type is
// ledger, parsed as YAML the way the index parses every note: a line-by-line
// scan matched an indented "type:" belonging to another key.
func ledgerTyped(rel string, head []byte) bool {
	return vault.Parse("", rel, string(head)).Kind == LedgerType
}

// Review is the latest verdict on one ruling. Retired drops it from what is
// handed over; otherwise Commit is where a person last confirmed it, and the
// drift is measured from there instead of from the checkpoint's commit.
type Review struct {
	Retired bool
	Commit  string
	TS      int64
}

// For applies a verdict to one checkpoint's copy of the ruling, and says
// whether that copy is retired and which commit to measure it from.
//
// A verdict covers only checkpoints recorded up to when it was given. A dead
// end retired and then recorded again by a later agent is a new finding —
// the file regressed, say — and dropping it on a text match would send the
// next agent straight back into it. Likewise a later restatement's commit is
// newer than the re-confirm, and measuring from the re-confirm would warn
// again about commits that agent already worked on top of.
func (r Review) For(checkpointTS int64, checkpointCommit string) (retired bool, commit string) {
	if r.TS == 0 || r.TS < checkpointTS {
		return false, checkpointCommit
	}
	if r.Retired {
		return true, checkpointCommit
	}
	if r.Commit != "" {
		return false, r.Commit
	}
	return false, checkpointCommit
}

// ReviewKey is how a reviewed ruling is matched back to its checkpoint entry:
// case and whitespace folded, the same key resume dedups rulings by, so a
// ruling restated in a later checkpoint with different wrapping is the same
// ruling reviewed once.
func ReviewKey(text string) string {
	return strings.ToLower(strings.Join(strings.Fields(text), " "))
}

func reviewedPath(vaultDir, scope string) string {
	return filepath.Join(vaultDir, CheckpointDir, filepath.FromSlash(safeScope(scope)), ReviewedFile)
}

// Reviews reads a scope's verdicts, keyed by ReviewKey. The file is append-only
// and the last line about a ruling wins, so re-confirming a ruling retired by
// mistake brings it back. A missing file is no verdicts, not an error.
func Reviews(vaultDir, scope string) (map[string]Review, error) {
	f, err := os.Open(reviewedPath(vaultDir, scope))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	out := map[string]Review{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		key, r, ok := parseReview(sc.Text())
		if ok {
			out[key] = r
		}
	}
	return out, sc.Err()
}

// A line is "- <RFC3339> retired: <ruling>" or
// "- <RFC3339> reconfirmed at <commit>: <ruling>". A line in any other shape —
// a heading, a hand-written aside — is skipped rather than refused: a person
// is free to annotate the file, and one odd line must not undo every verdict.
func parseReview(line string) (string, Review, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(line), "- ")
	if !ok {
		return "", Review{}, false
	}
	stamp, rest, ok := strings.Cut(rest, " ")
	if !ok {
		return "", Review{}, false
	}
	t, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		return "", Review{}, false
	}
	verdict, text, ok := strings.Cut(rest, ": ")
	if !ok || strings.TrimSpace(text) == "" {
		return "", Review{}, false
	}
	r := Review{TS: t.Unix()}
	switch {
	case verdict == "retired":
		r.Retired = true
	case strings.HasPrefix(verdict, "reconfirmed at "):
		r.Commit = strings.TrimSpace(strings.TrimPrefix(verdict, "reconfirmed at "))
		if r.Commit == "" {
			return "", Review{}, false
		}
	default:
		return "", Review{}, false
	}
	return ReviewKey(text), r, true
}

// Retire records that a ruling no longer holds.
func Retire(vaultDir, scope, ruling string) error {
	return appendReview(vaultDir, scope, "retired", ruling)
}

// Reconfirm records that a ruling still holds at commit.
func Reconfirm(vaultDir, scope, ruling, commit string) error {
	if strings.TrimSpace(commit) == "" {
		return fmt.Errorf("re-confirming needs the commit it holds at")
	}
	return appendReview(vaultDir, scope, "reconfirmed at "+commit, ruling)
}

func appendReview(vaultDir, scope, verdict, ruling string) error {
	text := strings.Join(strings.Fields(ruling), " ")
	if text == "" {
		return fmt.Errorf("no ruling to review")
	}
	// Under the vault lock: two `logos review --stale` runs (or one and a
	// person's editor-save) reading and rewriting the file at once would each
	// keep only their own line.
	g, err := vault.Lock(vaultDir, "reviewed")
	if err != nil {
		return err
	}
	defer g.Unlock()

	path := reviewedPath(vaultDir, scope)
	old, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if len(old) == 0 {
		old = []byte("# Rulings reviewed with `logos review --stale` — the last line about a ruling wins\n\n")
	} else if old[len(old)-1] != '\n' {
		old = append(old, '\n')
	}
	line := fmt.Sprintf("- %s %s: %s\n", time.Now().UTC().Format(time.RFC3339), verdict, text)
	return vault.WriteAtomic(path, append(old, line...))
}
