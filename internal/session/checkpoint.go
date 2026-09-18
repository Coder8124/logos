package session

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Coder8124/logos/internal/gitstate"
	"github.com/Coder8124/logos/internal/text"
	"github.com/Coder8124/logos/internal/vault"
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
	// The verified half. Failed says what was ruled out; these three say what the
	// next agent may stand on without checking it again.
	//
	// Prose cannot carry this. "Auth is done" is a claim, and it reads the same
	// whether a test demonstrated it or the last agent believed it while its
	// context was running out. An arriving agent that cannot tell those apart
	// either re-verifies everything or builds on a sentence, and the second is
	// how a session inherits a foundation that was never true.
	//
	// So a verified entry names its evidence alongside the claim ("rejects
	// expired tokens — go test ./internal/auth -run TestExpiry"), Commands are
	// the invocations that produced it, and Blockers are the standing refutation:
	// what is known broken, and what it stops.
	Verified  []string
	Blockers  []string
	Commands  []string
	Questions []string
	Files     []string
	Next      string
	// Git is the repository as it stood, read rather than reported. The half of
	// a checkpoint an agent cannot forget to fill in — see the comment in
	// Commit, and package gitstate.
	Git gitstate.State
	// HandoffTo names who this was left for. Empty for a plain checkpoint —
	// the difference is intent, not mechanism.
	HandoffTo string
	// AutoClosed marks a checkpoint nobody wrote — see CloseAbandoned. It exists
	// so this is never confused with a real handoff: an agent (or a person)
	// reading one of these later must be able to tell, from the file itself,
	// that the work was simply abandoned rather than deliberately wrapped up.
	AutoClosed bool
	// Auto marks a checkpoint built from the activity log when a session ended
	// without one — see WriteAuto. It records what ran, never what was learned,
	// so a reader must be able to tell it apart from an agent's own.
	Auto bool
	Slug string // vault slug, set once written
	TS   int64
}

// CheckpointDir is where checkpoints live inside the vault. A visible folder,
// not a dotfile: these are notes you are meant to be able to open, read, and
// put under version control alongside everything else.
const CheckpointDir = "sessions"

func (c Checkpoint) Empty() bool {
	return strings.TrimSpace(c.Task) == "" && strings.TrimSpace(c.State) == "" &&
		strings.TrimSpace(c.Next) == "" && len(c.Decisions) == 0 &&
		len(c.Failed) == 0 && len(c.Questions) == 0 &&
		// "This much is proven" and "this is broken" are thin checkpoints but
		// not empty ones: they are the part of a session the next agent would
		// otherwise have to re-establish by running the suite itself. Commands
		// deliberately do not count — a list of invocations with no claim
		// attached records what was typed, not what is true.
		len(c.Verified) == 0 && len(c.Blockers) == 0
}

// Commit writes the checkpoint to the vault and closes the project's open
// sessions. This is the commit in "working tree, then commit": until it is
// called, everything the agent recorded lives only in the rebuildable index and
// is not really saved.
//
// The project's uncommitted working notes are folded in — including any left by
// an agent that died mid-task — so an agent that only ever called note_progress
// still produces a useful checkpoint instead of an empty one.
//
// Project is a scope, so "kestrel/feature-x" writes into
// sessions/kestrel/feature-x/ and closes only that worktree's sessions. The
// caller decides the scope, exactly as it decides the project — see
// internal/mcpserver/scope.go.
func Commit(db *sql.DB, vaultDir string, c *Checkpoint) error {
	if strings.TrimSpace(c.Project) == "" {
		return fmt.Errorf("a checkpoint needs a project")
	}
	// Distinguish "you gave no project" from "that project name cannot be a
	// filename" — reporting the second as the first blames the caller for
	// omitting what they supplied.
	if safeScope(c.Project) == "" {
		return fmt.Errorf(
			"project name %q has no letters or digits to make a filename from", c.Project)
	}
	c.Project = safeScope(c.Project)
	if strings.TrimSpace(c.Agent) == "" {
		c.Agent = "agent"
	}
	if c.TS == 0 {
		c.TS = time.Now().Unix()
	}

	// Bind to this agent's session. The id becomes the checkpoint's filename, so
	// it has to name whoever is actually writing it.
	s, err := Current(db, c.Project, c.Agent)
	if err != nil {
		return err
	}
	c.Session = s.ID
	if c.Task == "" {
		c.Task = s.Task
	}

	// Observe the repository, rather than waiting to be told about it.
	//
	// Everything above this line is what the agent chose to say. This is what
	// was actually true — branch, commit, and what was still uncommitted — read
	// straight from git. An agent that forgets to describe its state cannot
	// forget this, which is the whole point: the model is no longer the only
	// path by which a checkpoint becomes useful.
	//
	// Only filled when the caller did not set it, so a replayed or imported
	// checkpoint keeps the state it was recorded with instead of being
	// overwritten by whatever the working tree happens to look like now.
	if c.Git.Empty() {
		c.Git = gitstate.Read(workingDir())
	}

	// Fold the project's uncommitted working notes into the record. They are
	// appended, not substituted: a checkpoint closes the sessions, so anything
	// left only in session_notes becomes unreachable — dropping them whenever
	// the agent also wrote a state paragraph would silently lose everything
	// note_progress collected.
	//
	// This agent's own session, plus any that has gone silent past
	// ParallelGrace. When work passes from an agent that died mid-task, its
	// findings are exactly what the next agent is building on, and they have to
	// end up in the durable record or they are lost the moment anyone
	// checkpoints. An agent that is still working is left alone — taking its
	// notes here is what left every parallel agent but the first with a
	// checkpoint that had no State at all.
	folded, err := foldableSessions(db, c.Project, s.ID, ParallelGrace)
	if err != nil {
		return err
	}
	notes, err := notesIn(db, folded)
	if err != nil {
		return err
	}
	if len(notes) > 0 {
		lines := make([]string, 0, len(notes))
		for _, n := range notes {
			// Attribute when more than one agent contributed: which of them
			// found a thing is part of the finding.
			if n.Agent != "" && n.Agent != c.Agent {
				lines = append(lines, "- ("+n.Agent+") "+n.Text)
			} else {
				lines = append(lines, "- "+n.Text)
			}
		}
		recorded := "Recorded during the session:\n" + strings.Join(lines, "\n")
		if strings.TrimSpace(c.State) == "" {
			c.State = recorded
		} else {
			c.State = strings.TrimSpace(c.State) + "\n\n" + recorded
		}
	}
	if c.Empty() {
		return fmt.Errorf("nothing to checkpoint — record some progress first")
	}

	prev, _ := Latest(vaultDir, c.Project)
	var follows string
	if prev != nil {
		follows = prev.Session
	}

	// Claim the filename before writing it. Start already refuses to reuse a
	// session id, but Current hands the *same* open session to every caller for
	// one project and agent — so two agents checkpointing at once both arrive
	// here holding one id, and both wrote to one path. WriteAtomic renames into
	// place, so the second silently replaced the first: a handoff nobody was
	// told had been lost, in the one file this product exists to keep.
	id, path, err := claimCheckpoint(vaultDir, c.Project, c.Agent, s.ID)
	if err != nil {
		return err
	}
	c.Session = id
	c.Slug = filepath.ToSlash(filepath.Join(CheckpointDir, c.Project, id))
	if err := vault.WriteAtomic(path, []byte(c.Markdown(follows))); err != nil {
		return err
	}
	if err := closeSessions(db, folded, c.Slug); err != nil {
		return err
	}
	// The notes are inside the checkpoint now, so the working-notes file has
	// done its job and its contents would otherwise be claimed as still
	// outstanding. Removed after the markdown is written, never before: a
	// failure above this line must leave the notes where they were.
	return removeNotes(vaultDir, c.Project)
}

// claimCheckpoint reserves a checkpoint filename and returns the id that goes
// with it.
//
// The reservation is an O_EXCL create, not a stat: two writers that both look
// and both find nothing is exactly the race this exists to close, and it has to
// hold across processes as well as goroutines, because two editors each running
// their own MCP server is the ordinary way this product is used. WriteAtomic
// then renames over the placeholder we already own.
//
// On a collision the clock walks forward a second at a time, the same way Start
// allocates a session id — the timestamp stops being the moment of the write and
// becomes an ordering key, which is all any reader uses it for. Sixty seconds is
// as far as it goes: past that, something is wrong that a different filename
// will not fix.
func claimCheckpoint(vaultDir, project, agent, id string) (string, string, error) {
	dir := filepath.Join(vaultDir, CheckpointDir, filepath.FromSlash(project))
	if err := vault.MkdirPrivate(dir); err != nil {
		return "", "", err
	}
	start, err := time.Parse("20060102-150405", firstN(id, 15))
	if err != nil {
		// An id from somewhere that does not carry a timestamp. Nothing to walk
		// forward, so take it or fail — better than inventing an ordering key.
		start = time.Now()
	}
	for i := range 60 {
		candidate := id
		if i > 0 {
			candidate = idFor(agent, start.Add(time.Duration(i)*time.Second))
		}
		path := filepath.Join(dir, candidate+".md")
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, vault.FileMode)
		if err == nil {
			f.Close()
			return candidate, path, nil
		}
		if !os.IsExist(err) {
			return "", "", fmt.Errorf("reserving %s: %w", path, err)
		}
	}
	return "", "", fmt.Errorf(
		"could not find a free checkpoint filename for %s in a minute of names starting at %s", project, id)
}

func firstN(s string, n int) string {
	return text.Truncate(s, n)
}

// Latest returns the most recent checkpoint for a project, or nil if there is
// none.
//
// It reads the vault directory rather than a table on purpose. Checkpoints are
// the one thing here that must survive `rm -rf .logos` — if resume depended on
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
	dir := filepath.Join(vaultDir, CheckpointDir, filepath.FromSlash(safeScope(project)))
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && IsCheckpointFile(e.Name()) {
			names = append(names, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))

	// Take the newest n that actually say something, rather than the newest n
	// files. A torn write — a crash or a full disk partway through a save —
	// leaves a file that parses to an empty checkpoint, and truncating the list
	// before parsing meant one such file at the front hid every intact
	// checkpoint behind it. `resume` then reported nothing at all for a project
	// with a year of history, which is the worst possible reading of a damaged
	// file: not "this one is broken" but "there is nothing here".
	out := make([]Checkpoint, 0, len(names))
	for _, name := range names {
		if n > 0 && len(out) >= n {
			break
		}
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue // a checkpoint we cannot read must not hide the ones we can
		}
		c := ParseCheckpoint(string(raw))
		if c.Empty() {
			continue
		}
		c.Slug = filepath.ToSlash(filepath.Join(CheckpointDir, safeScope(project), strings.TrimSuffix(name, ".md")))
		out = append(out, c)
	}
	return out, nil
}

// IsCheckpointFile decides whether a file in a session directory is one of
// ours.
//
// Not every .md here is a checkpoint: uncommitted.md holds working notes, and
// a user is free to leave a stray file of their own beside their history. The
// name is the test because the name is also the sort key — "most recent" is a
// reverse sort of these filenames, so anything not stamped with a timestamp
// does not merely add a bad row, it sorts to the front and becomes what
// `resume` reports as the last thing that happened.
func IsCheckpointFile(name string) bool {
	if !strings.HasSuffix(name, ".md") || len(name) < 9 || name[8] != '-' {
		return false
	}
	for i := range 8 {
		if name[i] < '0' || name[i] > '9' {
			return false
		}
	}
	return true
}

// Projects lists the projects that have at least one checkpoint. Worktree
// scopes are folders inside a project's, so they are not listed as projects of
// their own — which is what they are not.
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

// workingDir is where the agent is standing. A coding agent's cwd is the repo it
// is working in — that is the assumption the whole product makes — and an MCP
// server inherits it from the host that launched it.
func workingDir() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	return dir
}

// IsPlaceholder reports a Failed bullet that says nothing was ruled out.
// Agents fill the field with "none this session" rather than leave it empty,
// and stored as a bullet it read as a dead end: resume listed it as ruled out
// and before_you_try matched it to unrelated proposals.
func IsPlaceholder(s string) bool {
	s = strings.ToLower(strings.Trim(strings.TrimSpace(s), ".!-–— "))
	for _, suffix := range []string{" this session", " so far", " yet", " today"} {
		s = strings.TrimSuffix(s, suffix)
	}
	switch s {
	case "", "none", "nothing", "n/a", "na", "nil", "no", "nothing ruled out", "no failures", "no dead ends":
		return true
	}
	return false
}

// DropPlaceholders returns failed without its placeholder bullets, and how
// many it dropped so the caller can say so.
func DropPlaceholders(failed []string) ([]string, int) {
	var kept []string
	for _, f := range failed {
		if !IsPlaceholder(f) {
			kept = append(kept, f)
		}
	}
	return kept, len(failed) - len(kept)
}

// NextReadsAsMoreThanOneStep reports that `next` is not the single step the
// schema asks for — a conditional ("if asked, populate Appendix A, otherwise
// ship it") or a second sentence.
//
// The receipt is deliberately not a refusal. A checkpoint is written when an
// agent's context is running out, which is the worst possible moment to argue
// about grammar and lose the record; the writing agent reads fine to itself and
// only a different tool pays for the ambiguity, so the record is kept and the
// doubt is said out loud instead.
func NextReadsAsMoreThanOneStep(next string) bool {
	next = strings.TrimSpace(next)
	if next == "" {
		return false
	}
	lower := strings.ToLower(next)
	for _, word := range []string{"if ", "if,", "otherwise", "unless ", "depending on", "either way"} {
		if strings.HasPrefix(lower, word) || strings.Contains(lower, " "+word) {
			return true
		}
	}
	// A second sentence, not merely a full stop. "Cut the tag. Then watch the
	// run" is two steps; "run go test ./... and report" is one, and a naive
	// search for ". " reads its ellipsis as the end of a sentence.
	trimmed := strings.TrimRight(next, ".!?")
	for i, r := range trimmed {
		if r != '.' && r != '?' && r != '!' && r != ';' {
			continue
		}
		if i == 0 || i+1 >= len(trimmed) || trimmed[i+1] != ' ' {
			continue
		}
		if r == '.' && (trimmed[i-1] == '.' || trimmed[i-1] == '/') {
			continue
		}
		return true
	}
	return strings.Contains(trimmed, "\n")
}
