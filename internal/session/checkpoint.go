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
	"github.com/Coder8124/logos/internal/secret"
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
	Session string `json:"session,omitempty"`
	// HostSession is the id the *host* uses for this conversation — Claude
	// Code's hook session_id, or a transcript reader's own s.ID — never
	// Logos's own Session stem above. Session names the checkpoint file;
	// HostSession names the conversation that produced it, which is what
	// lets every checkpoint from one sitting be found together regardless of
	// how many times it was checkpointed. Empty when nothing told us: an
	// MCP host that gives no session_id (Cursor, Claude Desktop) leaves this
	// blank rather than guessing, which is more honest than a wrong id.
	HostSession string `json:"host_session,omitempty"`
	Project     string `json:"project,omitempty"`
	Agent       string `json:"agent,omitempty"`
	Task        string `json:"task,omitempty"`
	// Intent is why the task matters: the outcome it serves and the constraint
	// that shapes it. Task says what; a resume that only says what leaves the
	// next agent rereading the transcript to learn which trade-offs are open,
	// and picking the one the user already refused. It is written once per task
	// and handed forward to later checkpoints of it — see contextpack.
	Intent    string   `json:"intent,omitempty"`
	State     string   `json:"state,omitempty"`
	Decisions []string `json:"decisions,omitempty"`
	Failed    []string `json:"failed,omitempty"`
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
	Verified  []string `json:"verified,omitempty"`
	Blockers  []string `json:"blockers,omitempty"`
	Commands  []string `json:"commands,omitempty"`
	Questions []string `json:"questions,omitempty"`
	Files     []string `json:"files,omitempty"`
	Next      string   `json:"next,omitempty"`
	// Git is the repository as it stood, read rather than reported. The half of
	// a checkpoint an agent cannot forget to fill in — see the comment in
	// Commit, and package gitstate.
	Git gitstate.State `json:"git,omitempty"`
	// HandoffTo names who this was left for. Empty for a plain checkpoint —
	// the difference is intent, not mechanism.
	HandoffTo string `json:"handoff_to,omitempty"`
	// AutoClosed marks a checkpoint nobody wrote — see CloseAbandoned. It exists
	// so this is never confused with a real handoff: an agent (or a person)
	// reading one of these later must be able to tell, from the file itself,
	// that the work was simply abandoned rather than deliberately wrapped up.
	AutoClosed bool `json:"auto_closed,omitempty"`
	// Auto marks a checkpoint built from the activity log when a session ended
	// without one — see WriteAuto. It records what ran, never what was learned,
	// so a reader must be able to tell it apart from an agent's own.
	Auto bool `json:"auto,omitempty"`
	// Inferred is what a later agent read out of an auto record's transcript:
	// what it verified, ruled out and decided, each citing the turn it came from.
	// Kept apart from Verified, Failed and Decisions on purpose. Those are an
	// agent's own testimony, and the next agent treats a failed entry as a
	// paid-for ruling and will not re-try what it names; a reading of somebody
	// else's transcript is not that, however good the reader. Nil until an
	// agent distils the record.
	Inferred *Inference `json:"inferred,omitempty"`
	// Turns is how many turns the transcript behind an auto record held when
	// the record was written or last grown, as its harness's reader counts
	// them. A session can be resumed, and its transcript goes on past the
	// record: without the count, a reading served "the transcript" would cover
	// work this record never saw and put conclusions about it into this
	// record. Zero on a record that could not count, which is then not served.
	Turns int `json:"turns,omitempty"`
	// Redactions is what Commit masked before writing, so the caller can say
	// so. Not written to the file: the file carries the marker in place.
	Redactions []secret.Redaction `json:"-"`
	Slug       string             `json:"slug,omitempty"` // vault slug, set once written
	TS         int64              `json:"ts,omitempty"`
}

// Inference is a distillation of an auto record's transcript. By and At say
// who read it and when, because an unreviewed claim nobody can attribute is one
// nobody can weigh.
type Inference struct {
	By       string   `json:"by,omitempty"`
	At       int64    `json:"at,omitempty"`
	Verified []string `json:"verified,omitempty"`
	Failed   []string `json:"failed,omitempty"`
	Decided  []string `json:"decided,omitempty"`
	// Wanted is what the person asked for or corrected the agent toward, each
	// citing a turn of theirs: the session's intent, read rather than
	// summarised, since an agent's account of what it was asked is the guess
	// the auto record's quoted task line exists to avoid.
	Wanted []string `json:"wanted,omitempty"`
	// Turns is the record's Turns when it was read. A record that has grown
	// past it holds work nobody read, and is offered for reading again.
	Turns int `json:"turns,omitempty"`
}

// Empty reports whether the distillation kept nothing.
func (in *Inference) Empty() bool {
	return in == nil || len(in.Verified)+len(in.Failed)+len(in.Decided)+len(in.Wanted) == 0
}

// CheckpointDir is where checkpoints live inside the vault. A visible folder,
// not a dotfile: these are notes you are meant to be able to open, read, and
// put under version control alongside everything else.
const CheckpointDir = "sessions"

// ConvosDir is the reserved folder name directly under a scope — a project,
// or a project/worktree pair — that holds its conversations. Reserved, not
// inferred: a worktree folder and a conversation folder sit at the exact same
// depth and look identical on disk (both are just a directory name under a
// project), so without one fixed, never-a-worktree name marking where the
// conversations start, Scopes could not tell a sibling worktree from one of
// this project's own conversations, and would either miss real worktrees
// once their checkpoints moved a level deeper, or list every conversation
// folder as a phantom worktree scope of its own.
const ConvosDir = "_convos"

// UnattachedFolder is the conversation folder a checkpoint lands in when
// nothing told Logos which conversation produced it — no host id, no folder
// named after data nobody gave us (see Checkpoint.HostSession's doc comment
// on why guessing is worse than this).
const UnattachedFolder = "_unattached"

// convoFolder names the conversation folder a checkpoint belongs in: the
// host's own session id, sanitized the same way any other path segment is,
// or UnattachedFolder when nothing gave us one — hand-written checkpoints and
// checkpoints from hosts without hooks both land there, together, rather
// than each inventing a folder name nobody supplied.
func convoFolder(hostSession string) string {
	if s := safe(hostSession); s != "" {
		return s
	}
	return UnattachedFolder
}

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
	// An agent debugging an upload pastes the token into what it verified, and
	// the checkpoint is the file this product exists to keep and sync (#209).
	// Masked here, after the notes are folded and before anything is written,
	// so no path to the file skips it.
	c.Redactions = c.maskSecrets()
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
	convo := convoFolder(c.HostSession)
	id, path, err := claimCheckpoint(vaultDir, c.Project, c.Agent, s.ID, convo)
	if err != nil {
		return err
	}
	c.Session = id
	c.Slug = filepath.ToSlash(filepath.Join(CheckpointDir, c.Project, ConvosDir, convo, id))
	if err := vault.WriteAtomic(path, []byte(c.Markdown(follows))); err != nil {
		return err
	}
	if err := closeSessions(db, folded, c.Slug); err != nil {
		return err
	}
	// Rewritten from the sessions still open, not deleted outright. The folded
	// notes are inside the checkpoint now and would otherwise be claimed as
	// still outstanding — but a parallel agent's notes were deliberately not
	// folded, and this file is the only place they live in the vault. Deleting
	// it left them in session_notes and nowhere else, so the next `logos index`
	// on a fresh database dropped work the product had promised to keep.
	// flushNotesIn removes the file when nothing is left, which is the common
	// case of a single agent checkpointing.
	//
	// Written after the markdown, never before: a failure above this line must
	// leave the notes where they were.
	return flushNotesIn(db, vaultDir, c.Project)
}

// maskSecrets masks credential-shaped text in every field the agent wrote,
// in place, and reports what it masked.
func (c *Checkpoint) maskSecrets() []secret.Redaction {
	var found []secret.Redaction
	one := func(field string, s *string) {
		var r []secret.Redaction
		*s, r = secret.Mask(field, *s)
		found = append(found, r...)
	}
	all := func(field string, items []string) {
		for i := range items {
			one(field, &items[i])
		}
	}
	one("Task", &c.Task)
	one("Intent", &c.Intent)
	one("State", &c.State)
	all("Decisions", c.Decisions)
	all("Didn't work", c.Failed)
	all("Verified", c.Verified)
	all("Blockers", c.Blockers)
	all("Commands", c.Commands)
	all("Questions", c.Questions)
	all("Files", c.Files)
	one("Next", &c.Next)
	return found
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
//
// convo is the conversation folder (see convoFolder) the file is claimed
// inside, under ConvosDir — never directly inside the scope itself, so a
// fresh checkpoint is never the thing re-filing later finds "misfiled" and
// moves.
func claimCheckpoint(vaultDir, project, agent, id, convo string) (string, string, error) {
	scopeDir := filepath.Join(vaultDir, CheckpointDir, filepath.FromSlash(project))
	dir := filepath.Join(scopeDir, ConvosDir, convo)
	if err := vault.MkdirPrivate(dir); err != nil {
		return "", "", err
	}
	// O_EXCL only guards against a second writer into this same convo folder:
	// two conversations that land on the same timestamp-plus-agent id now
	// claim it in two different folders, and the OS sees no collision at all.
	// ResolvePath's citation-by-stem fallback matches by filename alone across
	// every convo, so a name taken twice in one scope would resolve to
	// whichever of the two it happened to reach first. Checked against the
	// whole scope, not just dir, to keep every name in it unique the way a
	// single flat directory used to guarantee for free.
	taken := map[string]bool{}
	if entries, err := CheckpointEntries(scopeDir); err == nil {
		for _, en := range entries {
			taken[en.Name] = true
		}
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
		if taken[candidate+".md"] {
			continue
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

// CheckpointEntry is one checkpoint file found under a scope's own
// directory, wherever it actually sits: directly in the scope (every
// checkpoint written before this migration, and anything hand-placed since),
// or inside one of the scope's conversation folders under ConvosDir
// (everything Commit writes now). Rel is the path to open, relative to the
// scope directory; Name is the checkpoint's own filename, kept separately
// because it is the sort key — a conversation folder is named after a host
// id or UnattachedFolder, not a timestamp, so sorting by Rel would interleave
// conversations by folder name instead of ordering checkpoints by when they
// were made. Exported so internal/deadend's own cache of ruling files can key
// on the same relative path History does, rather than hand-rolling a second
// walk that only knows the pre-ConvosDir layout.
type CheckpointEntry struct {
	Name string
	Rel  string
}

// CheckpointEntries finds every checkpoint file a scope's directory reaches,
// one level into ConvosDir and no further — a worktree scope's own directory
// is a *different* scope, reached by asking History for it directly, and
// descending into it here would merge two deliberately isolated histories
// into one.
func CheckpointEntries(dir string) ([]CheckpointEntry, error) {
	top, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []CheckpointEntry
	for _, e := range top {
		if !e.IsDir() && IsCheckpointFile(e.Name()) {
			out = append(out, CheckpointEntry{Name: e.Name(), Rel: e.Name()})
		}
	}
	convos, err := os.ReadDir(filepath.Join(dir, ConvosDir))
	if err != nil {
		return out, nil // no ConvosDir yet is the ordinary unmigrated case
	}
	for _, convo := range convos {
		if !convo.IsDir() {
			continue
		}
		inner, err := os.ReadDir(filepath.Join(dir, ConvosDir, convo.Name()))
		if err != nil {
			continue // one unreadable conversation must not hide the others
		}
		for _, f := range inner {
			if !f.IsDir() && IsCheckpointFile(f.Name()) {
				out = append(out, CheckpointEntry{
					Name: f.Name(),
					Rel:  filepath.ToSlash(filepath.Join(ConvosDir, convo.Name(), f.Name())),
				})
			}
		}
	}
	return out, nil
}

// History returns up to n checkpoints for a project, newest first.
func History(vaultDir, project string, n int) ([]Checkpoint, error) {
	dir := filepath.Join(vaultDir, CheckpointDir, filepath.FromSlash(safeScope(project)))
	entries, err := CheckpointEntries(dir)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name > entries[j].Name })

	// Take the newest n that actually say something, rather than the newest n
	// files. A torn write — a crash or a full disk partway through a save —
	// leaves a file that parses to an empty checkpoint, and truncating the list
	// before parsing meant one such file at the front hid every intact
	// checkpoint behind it. `resume` then reported nothing at all for a project
	// with a year of history, which is the worst possible reading of a damaged
	// file: not "this one is broken" but "there is nothing here".
	out := make([]Checkpoint, 0, len(entries))
	for _, en := range entries {
		if n > 0 && len(out) >= n {
			break
		}
		raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(en.Rel)))
		if err != nil {
			continue // a checkpoint we cannot read must not hide the ones we can
		}
		c := ParseCheckpoint(string(raw))
		if c.Empty() {
			continue
		}
		c.Slug = filepath.ToSlash(filepath.Join(CheckpointDir, safeScope(project), strings.TrimSuffix(en.Rel, ".md")))
		out = append(out, c)
	}
	return out, nil
}

// ByHostSession returns every checkpoint anywhere in the vault that carries
// the given host session id, oldest first — the whole follows chain one
// conversation produced, however many times it checkpointed and whatever
// scope it checkpointed under.
//
// `logos resume <session-id>` needs this because a conversation's project can
// change mid-work (a worktree spun up partway through) and because "what did
// this conversation do" is a question about the id, not about any one
// project's history. Built on Scopes and History rather than a parallel
// directory walk, so a torn file is skipped exactly as resume already skips
// one, and a checkpoint missing from Latest/History is never found here
// either.
func ByHostSession(vaultDir, hostSession string) ([]Checkpoint, error) {
	if strings.TrimSpace(hostSession) == "" {
		return nil, nil
	}
	scopes, err := Scopes(vaultDir)
	if err != nil {
		return nil, err
	}
	var out []Checkpoint
	for _, scope := range scopes {
		all, err := History(vaultDir, scope, 0)
		if err != nil {
			continue // one unreadable scope must not hide the others
		}
		for _, c := range all {
			if c.HostSession == hostSession {
				out = append(out, c)
			}
		}
	}
	// History returns newest first within a scope; the chain reads naturally
	// oldest first across scopes too. Sorted on Session, not TS: two
	// checkpoints claimed inside the same second still get filenames that
	// collide-and-increment in order (see claimCheckpoint), so the filename
	// is the finer clock and TS alone would leave same-second checkpoints in
	// whatever order the directory scan happened to find them.
	sort.Slice(out, func(i, j int) bool { return out[i].Session < out[j].Session })
	return out, nil
}

// Refile moves every checkpoint whose host_session frontmatter disagrees with
// the conversation folder it is physically sitting in, across the whole
// vault, and returns how many it moved.
//
// Frontmatter is the truth (see Checkpoint.HostSession's doc comment); the
// ConvosDir folder is a derived layout, rebuilt from it here exactly the way
// `logos index` rebuilds the cache from the vault. A file can end up in the
// wrong folder after an interrupted migration, a sync conflict copy, or a
// hand move — never after an ordinary Commit, which already files a fresh
// checkpoint where its own HostSession says it belongs.
//
// The new location is written before the old one is removed, so a crash
// between the two leaves the checkpoint readable twice — the next Refile call
// finishes the move and removes the stale copy — never a checkpoint that
// briefly existed nowhere.
func Refile(vaultDir string) (int, error) {
	scopes, err := Scopes(vaultDir)
	if err != nil {
		return 0, err
	}
	refiled := 0
	for _, scope := range scopes {
		dir := filepath.Join(vaultDir, CheckpointDir, filepath.FromSlash(safeScope(scope)))
		entries, err := CheckpointEntries(dir)
		if err != nil {
			continue // one unreadable scope must not hide the others
		}
		for _, en := range entries {
			oldPath := filepath.Join(dir, filepath.FromSlash(en.Rel))
			raw, err := os.ReadFile(oldPath)
			if err != nil {
				continue // unreadable file: nothing to refile, not a reason to stop
			}
			c := ParseCheckpoint(string(raw))
			if c.Empty() {
				continue // a torn write, not a misfiled checkpoint
			}
			wantRel := filepath.ToSlash(filepath.Join(ConvosDir, convoFolder(c.HostSession), en.Name))
			if en.Rel == wantRel {
				continue // already filed where its own frontmatter says it belongs
			}
			newPath := filepath.Join(dir, filepath.FromSlash(wantRel))
			if err := vault.WriteAtomic(newPath, raw); err != nil {
				return refiled, fmt.Errorf("re-filing %s: %w", en.Rel, err)
			}
			if err := os.Remove(oldPath); err != nil {
				return refiled, fmt.Errorf("re-filing %s: removing the old copy: %w", en.Rel, err)
			}
			refiled++
		}
	}
	return refiled, nil
}

// ResolvePath finds a checkpoint a stored citation names, wherever it
// actually sits in the vault now.
//
// slug is the citation as something else wrote it down: "sessions/<project>/
// <stem>", the only shape every citation before ConvosDir existed ever used,
// and still the shape already sitting in memories, checkpoints and ingest
// records nobody is going to rewrite. Tried literally first, since that is
// still correct for any checkpoint Refile has had no reason to move, and a
// direct stat is one syscall where a vault-wide search is a walk of every
// scope. Only on a miss does it fall back to a search by stem — the part of
// the citation that survives a move, because Refile renames the directory a
// checkpoint sits under and never the checkpoint's own filename.
func ResolvePath(vaultDir, slug string) (string, error) {
	rel := strings.TrimSuffix(filepath.ToSlash(strings.TrimSpace(slug)), ".md")
	direct := filepath.Join(vaultDir, filepath.FromSlash(rel)+".md")
	if _, err := os.Stat(direct); err == nil {
		return direct, nil
	}
	stem := rel
	scopeHint := ""
	if i := strings.LastIndexByte(rel, '/'); i >= 0 {
		stem = rel[i+1:]
		scopeHint = strings.TrimPrefix(rel[:i], CheckpointDir+"/")
	}
	name := stem + ".md"
	if !IsCheckpointFile(name) {
		return "", fmt.Errorf("%s is not a checkpoint in this vault", slug)
	}
	scopes, err := Scopes(vaultDir)
	if err != nil {
		return "", err
	}
	for _, scope := range scopes {
		// A citation that still names its project (scopeHint != "") only
		// searches that project's own scope — otherwise two projects whose
		// checkpoints happen to share a timestamp stem (the same second, or a
		// citation from before stems were scoped per convo) make the first
		// filename match across the whole vault an arbitrary pick instead of
		// the project the citation actually names.
		if scopeHint != "" && scope != scopeHint && !strings.HasPrefix(scope, scopeHint+"/") {
			continue
		}
		dir := filepath.Join(vaultDir, CheckpointDir, filepath.FromSlash(scope))
		entries, err := CheckpointEntries(dir)
		if err != nil {
			continue // one unreadable scope must not hide the others
		}
		for _, en := range entries {
			if en.Name == name {
				return filepath.Join(dir, filepath.FromSlash(en.Rel)), nil
			}
		}
	}
	return "", fmt.Errorf("%s is not a checkpoint in this vault", slug)
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

// Scopes lists every scope that actually holds a checkpoint, worktree scopes
// included and spelled the way resume takes them back ("shop/fix-auth").
//
// Projects deliberately returns only the top level, which is correct for
// "which projects exist" and wrong for every caller that wants something it
// can look history up under. safeScope preserves "/" on the way in, so a
// checkpoint written from a linked git worktree — including the worktrees
// Claude Code creates for its own agents — lands one level down, where a
// single ReadDir cannot see it. Everything that enumerated through Projects
// skipped that work while reporting a number as though it were complete:
// `logos projects` undercounted, list_projects named a parent with "(0
// checkpoints)", and internal/deadend never reached a ruling recorded from a
// worktree — the one thing that package exists to surface.
//
// Still two levels of *scope* — project, and optionally project/worktree —
// because two levels is the whole of the scope layout a caller ever asks
// History for by name. A third level exists on disk now, ConvosDir and the
// conversation folders under it, but it is not a scope of its own: it is
// where a scope's own checkpoints physically sit, and holdsRecord already
// looks there, so every caller that walks Scopes and then calls History on
// what it finds keeps seeing every checkpoint without needing to know
// conversation folders exist at all. ConvosDir itself is skipped as a
// worktree candidate below — without that, every project's own conversation
// folder would be mistaken for a sibling worktree once checkpoints moved a
// level deeper into it (see ConvosDir's doc comment).
func Scopes(vaultDir string) ([]string, error) {
	root := filepath.Join(vaultDir, CheckpointDir)
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if holdsRecord(filepath.Join(root, e.Name())) {
			out = append(out, e.Name())
		}
		nested, err := os.ReadDir(filepath.Join(root, e.Name()))
		if err != nil {
			continue // one unreadable project must not hide the others
		}
		for _, n := range nested {
			if !n.IsDir() || n.Name() == ConvosDir {
				continue
			}
			if holdsRecord(filepath.Join(root, e.Name(), n.Name())) {
				out = append(out, e.Name()+"/"+n.Name())
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// holdsRecord reports whether a directory holds a session record of its own:
// a checkpoint, or the working-notes file, either sitting in the directory
// directly (every checkpoint before this migration, and the notes file
// always) or inside one of its conversation folders under ConvosDir
// (everything Commit writes now). Without the second check, a migrated
// project's own directory holds no file directly any more — every checkpoint
// moved one level down into a conversation folder — and Scopes would stop
// seeing projects and worktrees the moment their checkpoints finished
// migrating, which is exactly backwards for a feature meant to make nothing
// harder to find.
//
// The notes file counts because `logos tried --ruled-out` writes a ruling as a
// note, deliberately, so a dead end can be recorded an hour into a session
// rather than only at its end — internal/deadend reads both. A scope with
// notes and no checkpoint yet is exactly the session still in progress, and
// dropping it would make the checker blind in its freshest case.
func holdsRecord(dir string) bool {
	if holdsRecordDirect(dir) {
		return true
	}
	convos, err := os.ReadDir(filepath.Join(dir, ConvosDir))
	if err != nil {
		return false
	}
	for _, c := range convos {
		if c.IsDir() && holdsRecordDirect(filepath.Join(dir, ConvosDir, c.Name())) {
			return true
		}
	}
	return false
}

// holdsRecordDirect is holdsRecord without the ConvosDir descent, for
// checking one specific directory (a scope's own, or one of its conversation
// folders) rather than the scope-plus-conversations holdsRecord answers.
func holdsRecordDirect(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if IsCheckpointFile(e.Name()) || e.Name() == NotesFile {
			return true
		}
	}
	return false
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

// DecisionsWithoutReason counts the decisions that state a choice and not what
// forced it. "use sqlite" reads as a preference, and the next agent with a
// different one reopens it; "use sqlite, because the vault stays on this
// machine" is a constraint it can check. The reason is the part only the
// transcript held, so without it resuming means rereading the conversation.
//
// A receipt, not a refusal, for the reason NextReadsAsMoreThanOneStep gives.
// And lenient on purpose: it looks for any sign of a reason — a connective, a
// dash or colon introducing one, a second sentence — rather than judging
// whether the reason is good. In a working vault almost every decision already
// carries one, and a nudge that fires on those is noise that trains agents to
// ignore the one that matters.
func DecisionsWithoutReason(decisions []string) int {
	n := 0
	for _, d := range decisions {
		if d = strings.TrimSpace(d); d != "" && !givesReason(d) {
			n++
		}
	}
	return n
}

// ClaimsDoneUnverified reports a state that says the work is finished while
// verified is empty. That is the most misleading checkpoint there is: the next
// agent reads "fixed and merged" as settled and builds on it, and the record
// holds nothing that shows it was ever run. A receipt, not a refusal, for the
// reason NextReadsAsMoreThanOneStep gives.
//
// A claim word counts only when nothing in the two words before it takes it
// back — "half done", "not fixed yet", "nothing finished" are the opposite
// claim, and flagging them would teach agents to ignore the nudge.
func ClaimsDoneUnverified(c Checkpoint) bool {
	if len(c.Verified) > 0 {
		return false
	}
	words := strings.FieldsFunc(strings.ToLower(c.State), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '\'')
	})
	for i, w := range words {
		if !doneWords[w] {
			continue
		}
		negated := false
		for j := max(0, i-2); j < i; j++ {
			if undoneWords[words[j]] || strings.HasSuffix(words[j], "n't") {
				negated = true
			}
		}
		if !negated {
			return true
		}
	}
	return false
}

var doneWords = map[string]bool{
	"done": true, "complete": true, "completed": true, "finished": true, "fixed": true,
	"passing": true, "passes": true, "shipped": true, "merged": true,
	"resolved": true,
}

var undoneWords = map[string]bool{
	"not": true, "no": true, "nothing": true, "never": true, "half": true,
	"almost": true, "nearly": true, "partly": true, "partially": true, "yet": true,
}

var reasonWords = map[string]bool{
	"because": true, "since": true, "so": true, "given": true, "per": true,
	"why": true, "otherwise": true, "unless": true, "until": true, "though": true,
	"matching": true, "stops": true, "avoids": true, "prevents": true,
	"keeps": true, "keeping": true, "as": true, "means": true, "saves": true, "lest": true,
}

var reasonPairs = []string{
	"to avoid", "to keep", "to stop", "to prevent", "to ensure", "to match",
	"to save", "to let", "in order", "rather than", "instead of", "due to",
}

func givesReason(d string) bool {
	for _, p := range []string{" — ", " – ", " - ", ": ", "; ", " (", ". "} {
		if strings.Contains(strings.TrimRight(d, ".!?"), p) {
			return true
		}
	}
	words := strings.FieldsFunc(strings.ToLower(d), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r == '\'')
	})
	for _, w := range words {
		if reasonWords[w] {
			return true
		}
	}
	joined := " " + strings.Join(words, " ") + " "
	for _, p := range reasonPairs {
		if strings.Contains(joined, " "+p+" ") {
			return true
		}
	}
	return false
}
