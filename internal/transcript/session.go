// Package transcript reads other coding agents' on-disk session logs and
// normalises them into a common shape.
//
// It is the front half of `brain ingest`. The thesis is
// "read transcripts, never replay them": a raw transcript resumed as context
// hands the next agent every abandoned approach as a live option, which is the
// stale-answer failure the continuity benchmark measures. So this package's job
// stops at reading — no model, no vault writes, no network. Distillation into a
// checkpoint candidate happens in distil.go and the queue in internal/session.
//
// Everything a reader returns is untrusted. A transcript is other agents' full
// text including whatever they read off the web, and it is the most hostile
// input this codebase accepts. Callers that put a Session in front of a model
// must frame it as evidence, never instructions (see internal/untrusted).
//
// Reading is strictly read-only: no function here opens a source file for
// write, ever. TestIngestNeverWritesToTheSourceTranscript asserts the mtimes.
package transcript

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
)

// Session is one agent session, normalised across harnesses.
type Session struct {
	Harness string // "claude-code", "codex", "cursor", ...
	ID      string // harness-native session id
	Path    string // absolute path to the source file
	Project string // best-effort repo/dir the session ran in
	Started int64
	Ended   int64
	Turns   []Turn
	Hash    string // content hash of the source, for idempotence

	// Skipped counts lines the reader could not parse but stepped over rather
	// than failing the whole session on. Surfaced, never swallowed (invariant
	// 3): a session with Skipped > 0 is reported as partially read.
	Skipped int
}

// Turn is one exchange in a session, in wall-clock order.
type Turn struct {
	Role   string // "user" | "assistant" | "tool"
	Text   string
	Tool   string // tool name, when Role == "tool"
	Status string // "ok" | "error", when known
	// Input is the tool invocation — the shell command, the file path — carried
	// on the tool turn so a harvest can list commands run and files touched
	// without a model. Empty when the harness did not record it.
	Input string
}

// reader is one harness's adapter. Native readers (Claude Code, Codex) are
// registered in this package; A3 registers a txcript-backed reader for every
// other format when the txcript binary is on PATH.
type reader interface {
	harness() string
	// root is the directory this reader scans, already resolved through env
	// overrides. "" means the directory does not exist on this machine.
	root() string
	// discover lists absolute paths to session files under root, newest first.
	discover() ([]string, error)
	// read parses one session file. A file it cannot open or that has no
	// usable content at all is an error; malformed individual lines are
	// counted into Session.Skipped, not raised.
	read(path string) (*Session, error)
}

// registry is keyed by harness name so a txcript-backed reader is one more
// entry, not a special case.
var registry = map[string]reader{}

func register(r reader) { registry[r.harness()] = r }

func init() {
	register(claudeCodeReader{})
	register(codexReader{})
	registerTxcriptReaders()
}

// Availability describes one harness and whether ingest can read it here.
type Availability struct {
	Harness  string
	Root     string
	Found    bool
	Reason   string // when !Found: why it was skipped, per invariant 3
	Sessions int    // discoverable session files, best effort
}

// Available reports every known harness and its status on this machine, sorted
// by name. A harness with a reader but no session directory, and a harness with
// no native reader and no txcript, both say why rather than vanishing from the
// list.
func Available() []Availability {
	out := make([]Availability, 0, len(registry))
	for name, r := range registry {
		a := Availability{Harness: name, Root: r.root()}
		switch {
		case r.root() == "":
			a.Reason = reasonNoRoot(r)
		default:
			paths, err := r.discover()
			if err != nil {
				a.Reason = err.Error()
			} else {
				a.Found = true
				a.Sessions = len(paths)
			}
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Harness < out[j].Harness })
	return out
}

// Harnesses lists every harness name ingest knows how to try, for help text
// and error messages.
func Harnesses() []string {
	out := make([]string, 0, len(registry))
	for name := range registry {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Sessions lists discoverable session file paths for one harness. An unknown
// harness, or one whose format needs txcript when txcript is absent, returns a
// SkipReason error naming the fix rather than an empty slice.
func Sessions(harness string) ([]string, error) {
	r, ok := registry[harness]
	if !ok {
		return nil, &SkipReason{Harness: harness, Why: "unknown harness; known: " + join(Harnesses())}
	}
	if r.root() == "" {
		return nil, &SkipReason{Harness: harness, Why: reasonNoRoot(r)}
	}
	return r.discover()
}

// ReadFile parses one session file with the reader for harness.
func ReadFile(harness, path string) (*Session, error) {
	r, ok := registry[harness]
	if !ok {
		return nil, &SkipReason{Harness: harness, Why: "unknown harness"}
	}
	return r.read(path)
}

// SkipReason is the "named and skipped, not silently dropped" error (invariant
// 3). It carries the harness and a sentence a user can act on.
type SkipReason struct {
	Harness string
	Why     string
}

func (e *SkipReason) Error() string { return e.Harness + ": " + e.Why }

func reasonNoRoot(r reader) string {
	if _, ok := r.(txcriptReader); ok {
		return "no native reader and txcript is not on PATH; install txcript for this format"
	}
	return "no session directory found (set the matching BRAIN_* override to point at one)"
}

// hashFile returns the hex SHA-256 of a file's bytes. The whole-file hash is
// the idempotence key: re-running ingest on an unchanged transcript produces
// the same hash and the queue skips it; an edited transcript hashes differently
// and becomes a new candidate rather than silently overwriting the old one.
func hashFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return hashBytes(b), nil
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// projectFromDir is the fallback attribution: the basename of a working
// directory. Harnesses that record no cwd get "" and the candidate lands
// unattributed for review to resolve — the plan is explicit that content must
// not be guessed at.
func projectFromDir(dir string) string {
	dir = filepath.Clean(dir)
	if dir == "" || dir == "." || dir == string(filepath.Separator) {
		return ""
	}
	return filepath.Base(dir)
}

func join(ss []string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}
