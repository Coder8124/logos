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
	r.Add(checkNotes(in.DB))
	r.Add(checkFreshness(in.Vault, in.DB))
	r.Add(checkEmbeddings(in.DB, in.Runtime))
	r.Add(checkRuntime(in.Runtime, in.EmbedModel))
	r.Add(checkContinuity(in.Vault))
	r.Add(checkAbandonment(in.DB))
	r.Add(checkCapture(in.DB, in.RetentionDays, in.KeepForever))
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
	var indexed sql.NullInt64
	if err := db.QueryRow("SELECT MAX(first_seen) FROM notes").Scan(&indexed); err != nil {
		c.State, c.Detail = Unknown, "could not read the index: "+err.Error()
		return c
	}
	if !indexed.Valid || indexed.Int64 == 0 {
		c.State, c.Detail = Failed, "vault has markdown but the index is empty"
		c.Fix = "run `brain index`"
		return c
	}
	lag := newest.Sub(time.Unix(indexed.Int64, 0))
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
	if len(abandoned) == 0 {
		c.State, c.Detail = OK, "none"
		return c
	}
	c.State = Failed
	lines := make([]string, 0, len(abandoned))
	for _, a := range abandoned {
		who := a.Agent
		if who == "" {
			who = "an agent"
		}
		lines = append(lines, fmt.Sprintf("%s (%s, %d notes, %s ago)",
			a.Project, who, a.Notes, roughly(time.Since(time.Unix(a.LastActivity, 0)))))
	}
	suffix := "s"
	if len(abandoned) == 1 {
		suffix = ""
	}
	c.Detail = fmt.Sprintf("%d session%s never checkpointed — %s", len(abandoned), suffix, strings.Join(lines, "; "))
	c.Fix = "run `brain sessions <project>` to see the notes, then checkpoint them or let the session go"
	return c
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
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
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
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d hours", int(d.Hours()))
	default:
		return fmt.Sprintf("%d days", int(d.Hours()/24))
	}
}
