package ingest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Coder8124/logos/internal/session"
	"github.com/Coder8124/logos/internal/transcript"
	"github.com/Coder8124/logos/internal/vault"
)

// Keeping the transcript a harvest read — when the user turned it on.
//
// A candidate points back at its source, and the source is not ours: Claude
// Code prunes old sessions, Codex rotates its rollouts, and a user clearing
// space deletes them. Once the file is gone nothing on the machine still says
// what the conversation actually said. So a harvest can copy the file, byte for
// byte, into the conversation's own folder in the vault — where everything else
// about that conversation already is.
//
// Off unless asked for. The ingest grant every user gave says a candidate is
// written to the vault and "never the transcript itself", and vaults get put
// in git: a default that started copying raw transcripts, pasted secrets and
// all, into a version-controlled folder would break the promise they agreed
// to without asking them again. So the setting rides in the same machine-local
// marker as that grant (ConsentPath), is set only by an explicit flag, and the
// flag says what it does the moment it is turned on.
//
// Verbatim, not redacted and not stripped of <private> spans: both edits are
// text surgery on a JSONL file, and one unclosed span or one mask that lands
// across a JSON string boundary turns the rest of the file into something no
// reader parses — a corrupted archive is worse than none, because it looks
// like one. The copy is written 0600 like the rest of the vault; the index
// never reads it (it is not markdown), and anything that parses it as a
// transcript applies keepPrivateOut exactly as it does to the host's own file.

// KeepTranscriptsWarning is said once, when keeping is turned on: after that
// every receipt names the copy, but this is the one place that says what the
// copy is.
const KeepTranscriptsWarning = "Harvests now keep a raw, unredacted copy of each transcript in its conversation's\n" +
	"folder in the vault — anything pasted into a session is in it. Store the vault as\n" +
	"you'd store the original transcripts: if it is in git, so are they.\n" +
	"Turn it off with `logos import --from transcripts --no-keep-transcripts`."

// KeepsTranscripts reports whether harvests on this machine copy the
// transcript into the vault. Anything short of an explicit yes in the marker —
// no marker, an unreadable one, a grant from before the setting existed, the
// setting without the grant it narrows — is no, because no is what those users
// agreed to.
func KeepsTranscripts(vaultDir string) bool {
	m, err := readConsent(vaultDir)
	if err != nil {
		return false
	}
	granted, _ := m["granted"].(string)
	on, _ := m[keepKey].(string)
	return granted != "" && on != ""
}

// SetKeepTranscripts turns keeping on or off, and reports whether that changed
// anything, so the caller warns on the turn and not on every run that repeats
// the flag. It refuses without the ingest grant: keeping is a narrowing of
// what the grant allowed, and a setting sitting in a marker that does not
// grant reading would read as permission nobody gave.
func SetKeepTranscripts(vaultDir string, on bool, now time.Time) (changed bool, err error) {
	m, err := readConsent(vaultDir)
	if err != nil {
		return false, fmt.Errorf("reading %s: %w", ConsentPath(vaultDir), err)
	}
	if g, _ := m["granted"].(string); g == "" {
		return false, errors.New("transcript ingest was never granted on this machine (`logos import --from transcripts` asks once)")
	}
	was, _ := m[keepKey].(string)
	if (was != "") == on {
		return false, nil
	}
	if on {
		m[keepKey] = now.UTC().Format(time.RFC3339)
	} else {
		delete(m, keepKey)
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return false, err
	}
	if err := vault.WriteAtomic(ConsentPath(vaultDir), b); err != nil {
		return false, fmt.Errorf("recording the setting in %s: %w", ConsentPath(vaultDir), err)
	}
	return true, nil
}

// keepKey holds when keeping was turned on, not a bare true, for the same
// reason "granted" does: a reader of the marker can tell when it changed.
const keepKey = "keep_transcripts"

// readConsent reads the marker as a loose map so that writing the setting back
// keeps every field it does not know about, the grant first among them.
func readConsent(vaultDir string) (map[string]any, error) {
	b, err := os.ReadFile(ConsentPath(vaultDir))
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	m := map[string]any{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// Archived is what one harvest did with its transcript. Err is set, and Path
// empty, when nothing was archived — and it says why, because a harvest that
// quietly kept nothing reads, a month later, as a transcript that was safe.
type Archived struct {
	Path      string // vault-relative, slash-separated
	Bytes     int64
	Unchanged bool // the vault already held exactly these bytes
	Err       error
	// Why is Err without the file names in it, so a harvest of two hundred
	// sessions whose host pruned them all prints one line with a count, not
	// two hundred lines that differ only in a path.
	Why string
}

func refused(why string, err error) Archived { return Archived{Err: err, Why: why} }

// Receipt is the archive's half of a harvest receipt, sized so the reader can
// see the vault grew, and saying "raw" every time so nobody mistakes the copy
// for the redacted candidate beside it.
func (a Archived) Receipt() string {
	switch {
	case a.Err != nil:
		return "raw transcript not archived: " + a.Err.Error()
	case a.Unchanged:
		return fmt.Sprintf("raw transcript already archived (%s), unchanged", Size(a.Bytes))
	default:
		return fmt.Sprintf("raw transcript archived (%s)", Size(a.Bytes))
	}
}

// ArchiveTranscript copies c's source transcript into its conversation folder
// (session.TranscriptDir). It is the copy, not the policy: callers check
// KeepsTranscripts. Whether it is "already archived" is read off the file
// itself — same length, same bytes — rather than recorded anywhere: there is
// nothing to rebuild after `logos index` because there is nothing but the file.
//
// An earlier copy is replaced only by a file that extends it. A host only ever
// appends, so a source that is shorter than the copy, or differs inside it, is
// a host that compacted or rewrote the session — and the copy is then the only
// record of what it said before. Replacing it would make the archive lose
// exactly what it exists to keep, so that is refused and said.
func ArchiveTranscript(vaultDir string, c Candidate) Archived {
	src := c.Source
	switch {
	case transcript.IsTxcriptSource(src):
		return refused("read through txcript, no file of its own to copy", errors.New("a txcript session is read through txcript and has no file of its own to copy"))
	case transcript.SourceFile(src) != src:
		// Cursor keeps every chat on the machine in one SQLite database.
		// Copying it per chat would put every other chat's text in this
		// conversation's folder, once per chat harvested.
		return refused("a chat in a database shared with every other chat", errors.New("this chat lives in a database shared with every other chat, which is not copied per conversation"))
	case src == "":
		return refused("no source file recorded", errors.New("the harvest recorded no source file"))
	}

	if rejected(vaultDir, c) {
		// The user said no to this session in review. Keeping its raw text
		// after that would hold on to the one thing they declined.
		return refused("its candidate was rejected in review", errors.New("its candidate was rejected in review"))
	}

	dir, err := session.TranscriptDir(vaultDir, c.Project, c.SessionID)
	if err != nil {
		return refused("no conversation folder to file it in", err)
	}
	ext := filepath.Ext(src)
	switch {
	case ext == "":
		ext = ".txt"
	case strings.EqualFold(ext, ".md"):
		// Every walker of sessions/ — the index, health, rename, the queue —
		// takes a .md file for a note. A host that writes its transcript as
		// markdown would otherwise have its raw copy indexed, searched and
		// packed into the next agent's context. The native extension is kept
		// in the name so a reader still sees what the host wrote.
		ext += ".txt"
	}
	dst := filepath.Join(dir, session.TranscriptStem+ext)
	rel := vaultRel(vaultDir, dst)

	info, err := os.Stat(src)
	if err != nil {
		why := "the host's file could not be read"
		if errors.Is(err, fs.ErrNotExist) {
			why = "the host's file is gone"
		}
		return refused(why, fmt.Errorf("reading %s: %w", src, err))
	}
	switch held := heldPrefix(src, dst); {
	case held == prefixEqual:
		return Archived{Path: rel, Bytes: info.Size(), Unchanged: true}
	case held == prefixDiffers:
		return refused("the host rewrote it, so the earlier copy is kept as it is", fmt.Errorf("%s no longer extends the copy at %s (the host rewrote it); the copy is kept as it is", src, rel))
	}
	if err := vault.MkdirPrivate(dir); err != nil {
		return refused("the vault folder could not be created", fmt.Errorf("creating %s: %w", vaultRel(vaultDir, dir), err))
	}
	n, err := copyFile(src, dst)
	if err != nil {
		return refused("the copy failed", fmt.Errorf("copying %s to %s: %w", src, rel, err))
	}
	return Archived{Path: rel, Bytes: n}
}

// What an existing copy is, relative to the source.
const (
	prefixNone    = iota // no copy yet: write one
	prefixGrown          // the copy is a strict prefix of the source: replace it
	prefixEqual          // the copy is the source, byte for byte
	prefixDiffers        // the copy is longer, or differs inside its length
)

// heldPrefix compares dst against the start of src. Length first, so a copy
// longer than its source is caught without reading a byte; otherwise the
// copy's bytes are compared against the same span of the source, because a
// rewritten file can keep or grow its length and still not be the copy we hold.
func heldPrefix(src, dst string) int {
	di, err := os.Stat(dst)
	if err != nil {
		return prefixNone
	}
	si, err := os.Stat(src)
	if err != nil || di.Size() > si.Size() {
		return prefixDiffers
	}
	a, err := os.Open(src)
	if err != nil {
		return prefixDiffers
	}
	defer a.Close()
	b, err := os.Open(dst)
	if err != nil {
		return prefixDiffers
	}
	defer b.Close()
	bufA, bufB := make([]byte, 64<<10), make([]byte, 64<<10)
	for left := di.Size(); left > 0; {
		n := int64(len(bufB))
		if left < n {
			n = left
		}
		if _, err := io.ReadFull(a, bufA[:n]); err != nil {
			return prefixDiffers
		}
		if _, err := io.ReadFull(b, bufB[:n]); err != nil {
			return prefixDiffers
		}
		if !bytes.Equal(bufA[:n], bufB[:n]) {
			return prefixDiffers
		}
		left -= n
	}
	if di.Size() == si.Size() {
		return prefixEqual
	}
	return prefixGrown
}

// rejected reports whether this session's candidate — or the sibling an edited
// transcript lands in — was rejected in review.
func rejected(vaultDir string, c Candidate) bool {
	for _, rel := range []string{c.RelPath(), c.variantRelPath()} {
		if got, err := readCandidate(filepath.Join(vaultDir, filepath.FromSlash(rel))); err == nil && got.Status == StatusRejected {
			return true
		}
	}
	return false
}

func vaultRel(vaultDir, p string) string {
	if rel, err := filepath.Rel(vaultDir, p); err == nil {
		return filepath.ToSlash(rel)
	}
	return p
}

// Size is a byte count the way a receipt says it: "41 KB", not 41983.
// Rounded up, so a file that exists never reads as "0 KB".
func Size(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%d KB", (n+1023)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
}
