// Package health answers "is this actually working", and is careful about the
// difference between a thing that is fine and a thing nobody looked at.
//
// The old `brain doctor` listed runtimes, tiers and voice engines. It never
// looked at the vault, the index, or whether any host was wired — so a user
// with an empty vault and a stale index got a clean bill of health, and the
// first sign of trouble was an agent answering as though it knew nothing.
//
// That is the same class of failure the hardening pass was built to find:
// silence presented as success. So every check here resolves to one of three
// states, and Unknown is a first-class answer rather than something folded into
// OK. "I could not check the index because there is no index" is useful; "ok"
// in its place is a lie that costs an afternoon.
package health

import (
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Coder8124/brain/internal/memory"
	"github.com/Coder8124/brain/internal/provider"
	"github.com/Coder8124/brain/internal/session"
	"github.com/Coder8124/brain/internal/setup"
)

// State is what a check concluded.
type State string

const (
	// OK means checked, and fine.
	OK State = "ok"
	// Failed means checked, and broken. Actionable.
	Failed State = "failed"
	// Unknown means not checked — a precondition was missing, so no claim is
	// made either way. Never use this for "probably fine".
	Unknown State = "unknown"
)

// A Check is one question and its answer.
type Check struct {
	Name   string
	State  State
	Detail string
	// Fix is what the user should do, when there is something to do. Empty for
	// checks that pass or that the user cannot act on.
	Fix string
}

// A Report is every check, in the order they were run.
type Report struct {
	Checks []Check
}

// Add appends a check.
func (r *Report) Add(c Check) { r.Checks = append(r.Checks, c) }

// Counts summarises the report, which is what a caller needs to decide an exit
// code or a headline.
func (r Report) Counts() (ok, failed, unknown int) {
	for _, c := range r.Checks {
		switch c.State {
		case OK:
			ok++
		case Failed:
			failed++
		default:
			unknown++
		}
	}
	return
}

// Healthy reports whether nothing is broken. Unknowns do not make a system
// unhealthy — they make it unverified, which is a different sentence.
func (r Report) Healthy() bool {
	_, failed, _ := r.Counts()
	return failed == 0
}

// Input is what the checks have to work with. Every field is optional: a nil DB
// or provider produces Unknown for the checks that need it, which is the whole
// point of the package.
type Input struct {
	Vault string
	// DB is an open index. Nil means the index could not be opened, so anything
	// derived from it is unknown rather than absent.
	DB *sql.DB
	// Runtime is the discovered local model runtime, or nil if none answered.
	Runtime    *provider.Provider
	EmbedModel string
	// RetentionDays and KeepForever describe the capture policy in force.
	RetentionDays int
	KeepForever   bool
}

// Run performs every check.
func Run(in Input) Report {
	var r Report
	r.Add(checkVault(in.Vault))
	r.Add(checkPrivacy(in.Vault))
	r.Add(checkNotes(in.DB))
	r.Add(checkFreshness(in.Vault, in.DB))
	r.Add(checkEmbeddings(in.DB, in.Runtime))
	r.Add(checkRuntime(in.Runtime, in.EmbedModel))
	r.Add(checkContinuity(in.Vault))
	r.Add(checkAbandonment(in.DB))
	r.Add(checkCapture(in.DB, in.RetentionDays, in.KeepForever))
	r.Add(checkMemoryReview(in.DB))
	r.Add(checkHosts())
	return r
}

// The vault is the product. If it is missing or unwritable, nothing else
// matters, so this runs first and says exactly which of the two it is.
func checkVault(dir string) Check {
	c := Check{Name: "vault"}
	if strings.TrimSpace(dir) == "" {
		c.State, c.Detail = Failed, "no vault path resolved"
		c.Fix = "set BRAIN_VAULT, or run `brain setup`"
		return c
	}
	info, err := os.Stat(dir)
	if os.IsNotExist(err) {
		c.State, c.Detail = Failed, dir+" does not exist"
		c.Fix = "run `brain setup --vault " + dir + "`"
		return c
	}
	if err != nil {
		c.State, c.Detail = Failed, err.Error()
		return c
	}
	if !info.IsDir() {
		c.State, c.Detail = Failed, dir+" is a file, not a directory"
		return c
	}
	// Readable is not enough: brain writes checkpoints here, and finding that
	// out at handoff time is finding out too late.
	probe := filepath.Join(dir, ".brain-write-probe")
	if err := os.WriteFile(probe, []byte("x"), 0o600); err != nil {
		c.State, c.Detail = Failed, dir+" is not writable: "+err.Error()
		c.Fix = "check permissions; checkpoints cannot be saved"
		return c
	}
	os.Remove(probe)
	c.State, c.Detail = OK, dir
	return c
}

func checkNotes(db *sql.DB) Check {
	c := Check{Name: "notes"}
	if db == nil {
		c.State, c.Detail = Unknown, "no index open, so the note count could not be read"
		c.Fix = "run `brain index`"
		return c
	}
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM notes").Scan(&n); err != nil {
		c.State, c.Detail = Unknown, "could not read the notes table: "+err.Error()
		return c
	}
	c.State = OK
	switch n {
	case 0:
		c.Detail = "none indexed yet"
		c.Fix = "add markdown to the vault, then run `brain index`"
	default:
		c.Detail = fmt.Sprintf("%d indexed", n)
	}
	return c
}

// staleBy is how far behind the index may fall before it is worth mentioning.
// A couple of minutes covers an editor save mid-session.
const staleBy = 2 * time.Minute

// Freshness compares the newest markdown in the vault against the newest thing
// the index knows about. This is the check that catches "I edited a note and
// the agent still quotes the old one", which otherwise looks like brain being
// wrong rather than brain being behind.
func checkFreshness(dir string, db *sql.DB) Check {
	c := Check{Name: "index"}
	if db == nil {
		c.State, c.Detail = Unknown, "no index open"
		c.Fix = "run `brain index`"
		return c
	}
	newest, err := newestMarkdown(dir)
	if err != nil {
		c.State, c.Detail = Unknown, "could not scan the vault: "+err.Error()
		return c
	}
	if newest.IsZero() {
		c.State, c.Detail = OK, "nothing to index"
		return c
	}
	// When the last pass ran, not what date the newest note claims.
	//
	// The obvious-looking MAX(first_seen) is a date parsed out of frontmatter,
	// so it is always midnight. Comparing a file's mtime against it made every
	// vault touched after midnight read as hours behind the moment after a
	// successful index — the check reported a real number that answered a
	// question nobody asked, and it answered it wrong every day.
	var synced sql.NullInt64
	if err := db.QueryRow("SELECT value FROM meta WHERE key = 'last_sync'").Scan(&synced); err != nil && err != sql.ErrNoRows {
		c.State, c.Detail = Unknown, "could not read the index: "+err.Error()
		return c
	}
	if !synced.Valid || synced.Int64 == 0 {
		// Either the index has never been built, or it was built by a version
		// that did not record this. Both are answered by the same one command,
		// and neither is evidence that anything is wrong.
		var notes int
		db.QueryRow("SELECT COUNT(*) FROM notes").Scan(&notes)
		if notes == 0 {
			c.State, c.Detail = Failed, "vault has markdown but the index is empty"
		} else {
			c.State, c.Detail = Unknown, "cannot tell when the index was last built"
		}
		c.Fix = "run `brain index`"
		return c
	}
	lag := newest.Sub(time.Unix(synced.Int64, 0))
	if lag > staleBy {
		c.State = Failed
		c.Detail = fmt.Sprintf("stale — newest note is %s ahead of the index", roughly(lag))
		c.Fix = "run `brain index`"
		return c
	}
	c.State, c.Detail = OK, "current"
	return c
}

func checkEmbeddings(db *sql.DB, rt *provider.Provider) Check {
	c := Check{Name: "embeddings"}
	if db == nil {
		c.State, c.Detail = Unknown, "no index open"
		return c
	}
	var notes, embedded int
	if err := db.QueryRow("SELECT COUNT(*) FROM notes").Scan(&notes); err != nil {
		c.State, c.Detail = Unknown, err.Error()
		return c
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM embeddings").Scan(&embedded); err != nil {
		c.State, c.Detail = Unknown, "no embeddings table: "+err.Error()
		return c
	}
	switch {
	case notes == 0:
		c.State, c.Detail = OK, "nothing to embed"
	case embedded == 0 && rt == nil:
		// Not a failure. This is the documented no-model mode, and search still
		// works lexically — saying "failed" here would send someone to fix
		// something that is behaving as designed.
		c.State = OK
		c.Detail = "none — no model runtime, so search is lexical"
		c.Fix = "start Ollama and run `brain index` for semantic search"
	case embedded < notes:
		c.State = OK
		c.Detail = fmt.Sprintf("%d of %d notes", embedded, notes)
		c.Fix = "run `brain index` to embed the rest"
	default:
		c.State, c.Detail = OK, fmt.Sprintf("all %d notes", notes)
	}
	return c
}

func checkRuntime(rt *provider.Provider, model string) Check {
	c := Check{Name: "model runtime"}
	if rt == nil {
		// Optional by design, so this is not Failed. Under the coding-agent
		// framing the host's own model does the generating and brain only ever
		// wanted embeddings.
		c.State = OK
		c.Detail = "none — continuity is unaffected, search is lexical"
		c.Fix = "install Ollama and pull " + model + " (274 MB) for semantic search"
		return c
	}
	c.State = OK
	c.Detail = rt.Name + " at " + rt.BaseURL
	return c
}

// Continuity is the product's core claim, and until now there was no way for a
// user to see whether it was happening. A host model that never calls
// checkpoint produces exactly the same silence as one that does not exist.
func checkContinuity(vault string) Check {
	c := Check{Name: "continuity"}
	if strings.TrimSpace(vault) == "" {
		c.State, c.Detail = Unknown, "no vault"
		return c
	}
	// A vault that is not there has no checkpoints, but "no checkpoints yet" is
	// the wrong sentence for it: it is the same line a brand-new working vault
	// prints, and it came out OK while every other vault-backed check on the
	// same report said unchecked. Nothing can be concluded about continuity
	// from a directory that does not exist, so say that instead.
	if _, err := os.Stat(vault); err != nil {
		c.State, c.Detail = Unknown, "the vault is not there, so checkpoints could not be read"
		c.Fix = "run `brain setup`"
		return c
	}
	latest, project, agent, err := latestCheckpoint(vault)
	if err != nil {
		c.State, c.Detail = Unknown, "could not read checkpoints: "+err.Error()
		return c
	}
	if latest.IsZero() {
		c.State = OK
		c.Detail = "no checkpoints yet"
		c.Fix = "ask an agent to checkpoint, or run `brain checkpoint <project>`"
		return c
	}
	who := agent
	if who == "" {
		who = "an agent"
	}
	c.State = OK
	// A checkpoint that does not name its project still counts as a checkpoint;
	// printing the empty string for it produced "5 hours ago — , by an agent".
	if project == "" {
		c.Detail = fmt.Sprintf("last checkpoint %s ago, by %s", roughly(time.Since(latest)), who)
		return c
	}
	c.Detail = fmt.Sprintf("last checkpoint %s ago — %s, by %s", roughly(time.Since(latest)), project, who)
	return c
}

// Abandonment is the failure checkContinuity cannot see. A checkpoint being
// recent says the *product* of continuity is happening; it says nothing about
// work that started and never made it that far. session.Uncommitted already
// holds those notes — they are not lost — but nothing surfaced them as a
// problem, so a session that died mid-task looked identical to one that simply
// had not been checkpointed yet. This is what tells the two apart.
func checkAbandonment(db *sql.DB) Check {
	c := Check{Name: "abandoned sessions"}
	if db == nil {
		c.State, c.Detail = Unknown, "no index open"
		return c
	}
	abandoned, err := session.FindAbandoned(db, session.AbandonAfter)
	if err != nil {
		// A vault that has never run a session command has no sessions table
		// yet — nothing has been abandoned because nothing has been tried.
		c.State, c.Detail = Unknown, "could not read sessions: "+err.Error()
		return c
	}
	// Only a session that is holding notes is a failure. An open session with
	// nothing in it is a row this index would not rebuild from the vault — no
	// note was written, so no work is at risk — and calling it broken made
	// doctor permanently red with a fix nobody could carry out: the offered
	// remedy is "see the notes, then checkpoint them", and there are no notes.
	// They are still counted out loud, because a check that quietly drops what
	// it saw is the other way to be wrong here.
	var holding []session.Abandoned
	empty := 0
	for _, a := range abandoned {
		if a.Notes == 0 {
			empty++
			continue
		}
		holding = append(holding, a)
	}
	if len(holding) == 0 {
		c.State = OK
		switch empty {
		case 0:
			c.Detail = "none"
		default:
			c.Detail = fmt.Sprintf("none holding work (%d empty session%s left open)", empty, pluralS(empty))
		}
		return c
	}
	c.State = Failed
	lines := make([]string, 0, len(holding))
	for _, a := range holding {
		who := a.Agent
		if who == "" {
			who = "an agent"
		}
		lines = append(lines, fmt.Sprintf("%s (%s, %d note%s, %s ago)",
			a.Project, who, a.Notes, pluralS(a.Notes), roughly(time.Since(time.Unix(a.LastActivity, 0)))))
	}
	c.Detail = fmt.Sprintf("%d session%s never checkpointed — %s",
		len(holding), pluralS(len(holding)), strings.Join(lines, "; "))
	c.Fix = "run `brain sessions <project>` to see the notes, then checkpoint them or let the session go"
	return c
}

// pluralS is the plain -s plural; plural() above is the y/ies one.
func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func checkCapture(db *sql.DB, retentionDays int, keepForever bool) Check {
	c := Check{Name: "capture"}
	if db == nil {
		c.State, c.Detail = Unknown, "no index open"
		return c
	}
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM events").Scan(&n); err != nil {
		c.State, c.Detail = Unknown, "no events table: "+err.Error()
		return c
	}
	if n == 0 {
		c.State, c.Detail = OK, "off — nothing recorded"
		return c
	}
	if keepForever {
		c.State = OK
		c.Detail = fmt.Sprintf("%d events, kept indefinitely", n)
		c.Fix = "set retention_days in config to bound this"
		return c
	}
	c.State = OK
	c.Detail = fmt.Sprintf("%d events, pruned after %d days", n, retentionDays)
	return c
}

// checkMemoryReview is the PRODUCT RULE applied to quarantine: a feature that
// silently queues machine-proposed memories and never says so is no better
// than the unreviewed writes it replaced — the queue just fills up somewhere
// nobody looks. This is what makes the backlog visible on every `brain
// doctor`, the same way stale capture or a stale index already are.
func checkMemoryReview(db *sql.DB) Check {
	c := Check{Name: "memory review"}
	if db == nil {
		c.State, c.Detail = Unknown, "no index open"
		return c
	}
	if err := memory.Init(db); err != nil {
		c.State, c.Detail = Unknown, "could not open the memory store: "+err.Error()
		return c
	}
	n, err := memory.PendingCount(db)
	if err != nil {
		c.State, c.Detail = Unknown, "could not read the review queue: "+err.Error()
		return c
	}
	if n == 0 {
		c.State, c.Detail = OK, "nothing pending"
		return c
	}
	c.State = OK
	c.Detail = fmt.Sprintf("%d memor%s awaiting review", n, plural(n))
	c.Fix = "run `brain review` to accept or reject them"
	return c
}

func isAre(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

// Hosts is the difference between "brain is installed" and "your agents can
// reach it", which are not the same thing and were never distinguished.
func checkHosts() Check {
	c := Check{Name: "agent hosts"}
	var wired []string
	for _, r := range setup.Plan(setup.Hosts()) {
		if r.Outcome == setup.Pending {
			wired = append(wired, r.Host)
		}
	}
	if len(wired) == 0 {
		c.State = Unknown
		c.Detail = "no MCP hosts detected on this machine"
		c.Fix = "install Claude Code, Cursor or Codex, then run `brain mcp install`"
		return c
	}
	// Detected is not the same as wired — Plan reports what is installed, not
	// what points at brain. Say what was actually established.
	c.State = OK
	c.Detail = "detected: " + strings.Join(wired, ", ")
	c.Fix = "run `brain doctor --integration` to prove they can reach this vault"
	return c
}

// --- helpers -----------------------------------------------------------------

func newestMarkdown(dir string) (time.Time, error) {
	var newest time.Time
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // an unreadable corner should not fail the whole check
		}
		if d.IsDir() && strings.HasPrefix(d.Name(), ".") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		return nil
	})
	return newest, err
}

// latestCheckpoint reads the most recent checkpoint across every project, off
// disk rather than from the index — the file is the record, and this check must
// work on a vault that has never been indexed.
func latestCheckpoint(vault string) (ts time.Time, project, agent string, err error) {
	// Walked, not listed. Checkpoints live at sessions/<project>/<id>.md, so
	// reading only the top level of sessions/ reports "no checkpoints yet" on a
	// vault full of them — which would make this check quietly useless in
	// exactly the case it exists to report on.
	dir := filepath.Join(vault, session.CheckpointDir)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return time.Time{}, "", "", nil
	}
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		// Not every .md under sessions/ is a checkpoint. uncommitted.md holds
		// working notes and is rewritten on every note_progress, so it is almost
		// always the newest file here — which made this check answer "last
		// checkpoint 5 hours ago" for a vault whose last actual checkpoint was
		// days old. That is the precise failure the continuity check exists to
		// catch, reported as its own opposite.
		if err != nil || d.IsDir() || !session.IsCheckpointFile(d.Name()) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.ModTime().After(ts) {
			ts = info.ModTime()
			project, agent = describeCheckpoint(path)
		}
		return nil
	})
	if err != nil {
		return time.Time{}, "", "", err
	}
	return ts, project, agent, nil
}

// describeCheckpoint pulls the project and agent out of a checkpoint's
// frontmatter. Best effort: a checkpoint that does not parse still counts as a
// checkpoint, it just cannot name itself.
func describeCheckpoint(path string) (project, agent string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(line, "project:"); ok {
			project = strings.TrimSpace(v)
		}
		if v, ok := strings.CutPrefix(line, "agent:"); ok {
			agent = strings.TrimSpace(v)
		}
		if line == "---" && project != "" {
			break
		}
	}
	return project, agent
}

// roughly renders a duration the way a person would say it.
func roughly(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "moments"
	case d < time.Hour:
		return count(int(d.Minutes()), "minute")
	case d < 48*time.Hour:
		return count(int(d.Hours()), "hour")
	default:
		return count(int(d.Hours()/24), "day")
	}
}

// count saves the "1 hours" that makes a tool feel unfinished.
func count(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

// checkPrivacy reports what the rest of the machine can read.
//
// Everything Logos knows lives in one directory in the user's home. On a
// personal laptop that is nobody but them; on a shared box, a work machine with
// a management agent, or anything with another account on it, the mode bits are
// the only thing standing between a second user and every prompt the first one
// typed. That is worth one line in `brain doctor` whether or not it is worth
// worrying about, because "who can read this" is not a question you can answer
// by looking at the app.
//
// It reports rather than repairs. New files and directories are created private
// (see internal/vault.FileMode), but a vault that predates that, or one the user
// deliberately opened up to sync it, is theirs — silently chmod-ing somebody's
// filesystem is exactly the kind of unrequested help this product does not do.
// So: name the paths, give the command, let them decide.
func checkPrivacy(dir string) Check {
	c := Check{Name: "privacy"}
	if strings.TrimSpace(dir) == "" {
		c.State, c.Detail = Unknown, "no vault path resolved"
		return c
	}
	info, err := os.Stat(dir)
	if err != nil {
		c.State, c.Detail = Unknown, "vault not readable: "+err.Error()
		return c
	}
	// Name what is actually exposed rather than the directory alone. "0755 on a
	// folder" means nothing to most people; "your prompt log and the database
	// holding every note" means something.
	//
	// The contents are checked even when the directory itself is locked down,
	// and that is the whole point of the list. index.Open sets the mode on
	// index.db advisorily — `_ = vault.PrivateSiblings(...)`, with a comment
	// naming this check as what would catch a failure — so answering from the
	// directory's mode alone reported "readable only by you" over a 0644 copy of
	// every note, memory and checkpoint in the vault. A private directory is
	// also one chmod, one sync client or one backup away from not being one.
	var open []string
	for _, rel := range []string{"activity", ".brain/index.db", ".brain/index.db-wal", "memories", "sessions"} {
		p := filepath.Join(dir, rel)
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		if fi.Mode().Perm()&0o077 != 0 {
			open = append(open, rel)
		}
	}
	if info.Mode().Perm()&0o077 == 0 && len(open) == 0 {
		c.State, c.Detail = OK, "the vault is readable only by you"
		return c
	}

	c.State = Failed
	if info.Mode().Perm()&0o077 != 0 {
		c.Detail = fmt.Sprintf("%s is readable by other users on this machine", dir)
		if len(open) > 0 {
			c.Detail += " — so is " + strings.Join(open, ", ")
		}
	} else {
		// The directory is closed but something inside it is not. Say so
		// precisely: the fix is the same command, but "your vault is fine except
		// for the file holding all of it" is a different sentence.
		c.Detail = fmt.Sprintf("%s is private, but %s inside it %s readable by other users on this machine",
			dir, strings.Join(open, ", "), isAre(len(open)))
	}
	c.Fix = "run `chmod -R go-rwx " + dir + "` if this machine has other accounts on it"
	return c
}
