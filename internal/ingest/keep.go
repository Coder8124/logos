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

	"github.com/Coder8124/logos/internal/auto"
	"github.com/Coder8124/logos/internal/session"
	"github.com/Coder8124/logos/internal/transcript"
	"github.com/Coder8124/logos/internal/vault"
)

// Keeping the transcript a harvest read.
//
// A candidate points back at its source, and the source is not ours: Claude
// Code prunes old sessions, Codex rotates its rollouts, and a user clearing
// space deletes them. Once the file is gone nothing on the machine still says
// what the conversation actually said. So Logos copies the file into the
// conversation's own folder in the vault — where everything else about that
// conversation already is.
//
// On unless turned off, and redacted unless that is turned off too. It was an
// opt-in, and an opt-in nobody knew about kept nothing: the conversation a
// user most wanted back was the one their host had already pruned. What made
// the copy safe to default on is that it no longer holds what the user pasted
// into the session. Vaults get put in git, and a raw copy would have put a
// pasted token in a version-controlled folder; so the `redact` switch (also on
// by default, see internal/auto) masks secrets and <private> spans on the way
// in (redactcopy.go). Redaction fails closed: a transcript that cannot be
// redacted is not kept at all, never kept raw instead.
//
// The copy is written 0600 like the rest of the vault; the index never reads
// it (it is not markdown), and anything that parses it as a transcript applies
// keepPrivateOut exactly as it does to the host's own file.

// KeepTranscriptsWarning is said when keeping is turned on by hand, and is the
// one place that says what the copy is. It reads the redact switch: telling a
// user with redact off that their copies are masked is the one claim here that
// would put a secret in git on Logos's word.
func KeepTranscriptsWarning(vaultDir string) string {
	how := "with secrets and <private> spans masked (`logos config auto config redact off`\n" +
		"keeps it raw)"
	if !auto.On(vaultDir, auto.Redact) {
		how = "raw, because redact is off (`logos config auto config redact on` masks\n" +
			"secrets in new copies)"
	}
	return "Logos keeps a copy of each transcript it reads in its conversation's folder in the\n" +
		"vault, " + how + ". Store the vault as you'd store the original transcripts: if it is in\n" +
		"git, so are they. Turn it off with `logos import --from transcripts --no-keep-transcripts`."
}

// ArchiveNoticeTopic is the "said:" line in auto.md for the one-time notice a
// user who agreed to the old wording gets on the first archive.
const ArchiveNoticeTopic = "archive"

// ArchiveNotice is the one-time notice, empty once shown. Someone who granted
// ingest when it said "never the transcript itself" has not been told that
// transcripts are now kept, and finding a copy later is the surprise to avoid.
// Shown on the first archive that kept something — callers ask only after one
// did, since a notice spent on a failed archive is one nobody acted on — and
// only where a person reads it, never a server log. Recorded in the vault
// (auto.md), not the index.
func ArchiveNotice(vaultDir string) string {
	if auto.Said(vaultDir, ArchiveNoticeTopic) {
		return ""
	}
	msg := "Logos now keeps a copy of each transcript it reads in the vault, with secrets and <private> spans masked,\n" +
		"so a conversation outlives the host's own pruning. `logos config auto config archive off` stops it;\n" +
		"`... redact off` keeps copies raw. This is shown once."
	if !auto.On(vaultDir, auto.Redact) {
		msg = "Logos now keeps a copy of each transcript it reads in the vault, raw, because redact is off,\n" +
			"so a conversation outlives the host's own pruning. `logos config auto config archive off` stops it;\n" +
			"`... redact on` masks secrets in new copies. This is shown once."
	}
	if err := auto.MarkSaid(vaultDir, ArchiveNoticeTopic); err != nil {
		msg += fmt.Sprintf("\n(could not record that this was shown, so it will repeat: %v)", err)
	}
	return msg
}

// KeepsTranscripts reports whether transcripts are copied into the vault. A
// switch set by hand in auto.md wins; then an old explicit opt-in in the
// consent marker, which must keep meaning on even under `auto off`; then the
// default, which is on. An unreadable auto.md is no, as it is for every
// switch: the answer is somebody's, and cannot be seen.
func KeepsTranscripts(vaultDir string) bool {
	c, err := auto.Load(vaultDir)
	if err != nil {
		return false
	}
	if v, ok := c.Hand[auto.Archive]; ok {
		return v
	}
	if m, err := readConsent(vaultDir); err == nil {
		if on, _ := m[keepKey].(string); on != "" {
			return true
		}
	}
	return c.On(auto.Archive)
}

// SetKeepTranscripts turns keeping on or off (the `archive` switch) and reports
// whether that changed anything, so the caller warns on the turn and not on
// every run that repeats the flag. It no longer needs the ingest grant: the
// setting narrows nothing, the CLI asks for the grant before it gets here, and
// the other readers of a transcript carry their own gates. The old marker key
// is dropped, or turning keeping off would be undone by it.
func SetKeepTranscripts(vaultDir string, on bool, now time.Time) (changed bool, err error) {
	was := KeepsTranscripts(vaultDir)
	if err := auto.Set(vaultDir, string(auto.Archive), on); err != nil {
		return false, fmt.Errorf("recording the setting in %s: %w", auto.Path(vaultDir), err)
	}
	m, err := readConsent(vaultDir)
	if err != nil {
		return false, fmt.Errorf("reading %s: %w", ConsentPath(vaultDir), err)
	}
	if _, ok := m[keepKey]; ok {
		delete(m, keepKey)
		b, err := json.MarshalIndent(m, "", "  ")
		if err != nil {
			return false, err
		}
		if err := vault.WriteAtomic(ConsentPath(vaultDir), b); err != nil {
			return false, fmt.Errorf("recording the setting in %s: %w", ConsentPath(vaultDir), err)
		}
	}
	return was != on, nil
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
	Raw       bool // written with `redact` off: byte for byte what the host wrote
	Replaced  bool // an earlier raw copy was replaced by the redacted one
	Secrets   int
	Private   int
	Err       error
	// Why is Err without the file names in it, so a harvest of two hundred
	// sessions whose host pruned them all prints one line with a count, not
	// two hundred lines that differ only in a path.
	Why string
}

func refused(why string, err error) Archived { return Archived{Err: err, Why: why} }

// Receipt is the archive's half of a harvest receipt, sized so the reader can
// see the vault grew, saying how many secrets were masked, and saying "raw"
// when it was not, so nobody mistakes an unmasked copy for a masked one.
func (a Archived) Receipt() string {
	kind := "transcript"
	if a.Raw {
		kind = "raw transcript"
	}
	switch {
	case a.Err != nil:
		return "transcript not archived: " + a.Err.Error()
	case a.Unchanged:
		return fmt.Sprintf("%s already archived (%s), unchanged", kind, Size(a.Bytes))
	case a.Raw:
		return fmt.Sprintf("raw transcript archived (%s)", Size(a.Bytes))
	}
	r := fmt.Sprintf("transcript archived (%s), %s redacted", Size(a.Bytes), fmt.Sprintf("%d secret%s", a.Secrets, plural(a.Secrets)))
	if a.Private > 0 {
		r += ", " + fmt.Sprintf("%d <private> span%s kept out", a.Private, plural(a.Private))
	}
	if a.Replaced {
		r += ", replacing an earlier copy that held what is now masked"
	}
	return r
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
	return archiveTranscript(vaultDir, c, false)
}

// archiveTranscript is ArchiveTranscript, and with quick an existing redacted
// copy is extended rather than rebuilt (extendRedacted) when it can be.
func archiveTranscript(vaultDir string, c Candidate, quick bool) Archived {
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
	if err := vault.MkdirPrivate(dir); err != nil {
		return refused("the vault folder could not be created", fmt.Errorf("creating %s: %w", vaultRel(vaultDir, dir), err))
	}
	if !auto.On(vaultDir, auto.Redact) {
		return archiveRaw(vaultDir, src, dst, rel, info.Size())
	}

	if quick {
		if a, ok := extendRedacted(src, dst, rel); ok {
			return a
		}
	}

	// Redacted. The redacted stream of the source is built first, in a temp
	// file, and only a finished one is ever renamed into place: a failure
	// anywhere in redacting leaves the copy that was there as it was and keeps
	// nothing new, and never falls back to the raw bytes, because the raw
	// bytes are the thing that was not to be kept.
	tmp, st, n, err := redactToTemp(src, dir, filepath.Base(dst))
	if err != nil {
		return refused("it could not be redacted, so nothing was kept", fmt.Errorf("redacting %s failed, so no copy was kept: %w", src, err))
	}
	defer os.Remove(tmp)
	a := Archived{Path: rel, Bytes: n, Secrets: st.Secrets, Private: st.Private}
	switch heldRedacted(tmp, dst) {
	case prefixEqual:
		a.Unchanged = true
		return a
	case prefixDiffers:
		// Not today's redacted copy. An earlier raw copy, or one an older
		// redactor made before a detector learned a new shape, is the same
		// conversation masked less, and replacing it masks more: what
		// redacting it again gives is where today's stream begins. Anything
		// else is a host that rewrote its file (or a rule that now masks
		// less), and the copy is then the only record of what it said.
		if !extendsOnceRedacted(tmp, dst) {
			return refused("the host rewrote it, so the earlier copy is kept as it is", fmt.Errorf("%s no longer extends the copy at %s (the host rewrote it, or a redaction rule changed); the copy is kept as it is", src, rel))
		}
		a.Replaced = true
	}
	if err := os.Rename(tmp, dst); err != nil {
		return refused("the copy failed", fmt.Errorf("moving the redacted copy into %s: %w", rel, err))
	}
	return a
}

// archiveRaw is the `redact off` path: the host's bytes, as they were.
func archiveRaw(vaultDir, src, dst, rel string, size int64) Archived {
	switch held := heldPrefix(src, dst); {
	case held == prefixEqual:
		return Archived{Path: rel, Bytes: size, Unchanged: true, Raw: true}
	case held == prefixDiffers:
		if tmp, _, _, err := redactToTemp(src, filepath.Dir(dst), filepath.Base(dst)); err == nil {
			made := heldRedacted(tmp, dst)
			os.Remove(tmp)
			if made != prefixDiffers {
				return refused("the earlier copy was made with redaction on and is kept", fmt.Errorf("the copy at %s was made with redaction on; it is kept as it is (turn redact back on to extend it)", rel))
			}
		}
		return refused("the host rewrote it, so the earlier copy is kept as it is", fmt.Errorf("%s no longer extends the copy at %s (the host rewrote it); the copy is kept as it is", src, rel))
	}
	n, err := copyFile(src, dst)
	if err != nil {
		return refused("the copy failed", fmt.Errorf("copying %s to %s: %w", src, rel, err))
	}
	return Archived{Path: rel, Bytes: n, Raw: true}
}

// ArchiveForRecord keeps the transcript a session-end record was just built
// from, and returns the line that says so ("" when keeping is off). This is the
// one transcript-reading path that archives without the ingest grant: it
// already reads the transcript, gated by the `checkpoint` switch, and keeping
// is now on by default, so it is the path that fires for a user who never ran
// `logos import`. Only a session that had a record written or grown comes
// here, so a transcript the user never worked in is not copied. A refusal is
// returned as text, not dropped (invariant 4).
//
// Quick, because the sweep runs it inside resume. No one-time notice: the
// session-end path writes to a server log nobody reads, so Sweep adds it to
// what resume prints, and kept says whether there is anything to add it to.
func ArchiveForRecord(vaultDir string, s *transcript.Session) (receipt string, kept bool) {
	if s == nil || !KeepsTranscripts(vaultDir) {
		return "", false
	}
	a := archiveTranscript(vaultDir, Candidate{Harness: s.Harness, SessionID: s.ID, Source: s.Path, Project: s.Project}, true)
	return a.Receipt(), a.Err == nil
}

// What an existing copy is, relative to the source.
const (
	prefixNone    = iota // no copy yet: write one
	prefixGrown          // the copy is a strict prefix of the source: replace it
	prefixEqual          // the copy is the source, byte for byte
	prefixDiffers        // the copy is longer, or differs inside its length
)

// heldPrefix compares dst against the start of src.
func heldPrefix(src, dst string) int {
	di, err := os.Stat(dst)
	if err != nil {
		return prefixNone
	}
	return heldPrefixN(src, dst, di.Size())
}

// heldPrefixN compares the first n bytes of dst against the start of src.
// Length first, so a copy longer than its source is caught without reading a
// byte; otherwise the copy's bytes are compared against the same span of the
// source, because a rewritten file can keep or grow its length and still not
// be the copy we hold.
func heldPrefixN(src, dst string, n int64) int {
	if _, err := os.Stat(dst); err != nil {
		return prefixNone
	}
	si, err := os.Stat(src)
	if err != nil || n > si.Size() {
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
	for left := n; left > 0; {
		k := int64(len(bufB))
		if left < k {
			k = left
		}
		if _, err := io.ReadFull(a, bufA[:k]); err != nil {
			return prefixDiffers
		}
		if _, err := io.ReadFull(b, bufB[:k]); err != nil {
			return prefixDiffers
		}
		if !bytes.Equal(bufA[:k], bufB[:k]) {
			return prefixDiffers
		}
		left -= k
	}
	if n == si.Size() {
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
