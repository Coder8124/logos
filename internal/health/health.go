// Package health answers "is this actually working", and is careful about the
// difference between a thing that is fine and a thing nobody looked at.
//
// The old `logos doctor` listed runtimes, tiers and voice engines. It never
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
	"os"
	"strings"
	"time"

	"github.com/Coder8124/logos/internal/ingest"
	"github.com/Coder8124/logos/internal/memory"
	"github.com/Coder8124/logos/internal/provider"
	"github.com/Coder8124/logos/internal/session"
	"github.com/Coder8124/logos/internal/setup"
	"github.com/Coder8124/logos/internal/transcript"
	"github.com/Coder8124/logos/internal/vault"
)

// State is what a check concluded.
type State string

const (
	// OK means checked, and fine.
	OK State = "ok"
	// Failed means checked, and broken. Actionable.
	Failed State = "failed"
	// Warn means checked, working, and holding a backlog — a chore, not a
	// defect. Sessions nobody checkpointed, proposals nobody reviewed: work
	// waiting on the user, which has to be visible without being graded as
	// breakage. Without this tier the three chore checks each picked a side and
	// disagreed: one was Failed, so `logos doctor` exited 1 forever on a
	// perfectly healthy install, and the other two were OK, which hid them.
	Warn State = "warn"
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
func (r Report) Counts() (ok, warn, failed, unknown int) {
	for _, c := range r.Checks {
		switch c.State {
		case OK:
			ok++
		case Warn:
			warn++
		case Failed:
			failed++
		default:
			unknown++
		}
	}
	return
}

// Healthy reports whether nothing is broken. Unknowns do not make a system
// unhealthy — they make it unverified, which is a different sentence. Nor do
// warnings: a queue waiting to be reviewed is the product working, and a
// verdict that cannot tell that from a corrupt index is a verdict nobody reads.
func (r Report) Healthy() bool {
	_, _, failed, _ := r.Counts()
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
	// Runtime is the runtime the server would use, or nil if none answered.
	Runtime *provider.Provider
	// Configured is LOGOS_RUNTIME, when set. With Runtime nil it means the
	// runtime the user named is down, which is not the same as having none.
	Configured string
	EmbedModel string
	// Hosts drives the duplicate-registration check. Nil means "not asked" —
	// distinct from "asked and found nothing" — and reports Unknown, because
	// listing a host's real registrations means shelling out to another
	// application's CLI, which a caller should opt into rather than pay for on
	// every unrelated health check.
	Hosts []setup.Host
	// Version is this binary's release, for comparing against the Claude Code
	// plugin's. "dev" or empty means there is nothing to compare.
	Version string
	// Self is this binary's path with symlinks resolved, for finding another
	// logos that runs instead of it. Empty skips that check.
	Self string
}

// Run performs every check.
func Run(in Input) Report {
	var r Report
	r.Add(checkVault(in.Vault))
	r.Add(checkPrivacy(in.Vault))
	r.Add(checkNotes(in.Vault, in.DB))
	r.Add(checkDurability(in.DB))
	r.Add(checkFreshness(in.Vault, in.DB))
	r.Add(checkEmbeddings(in.DB, in.Runtime))
	r.Add(checkRuntime(in.Runtime, in.Configured, in.EmbedModel))
	r.Add(checkContinuity(in.Vault))
	r.Add(checkIngest(in.Vault))
	r.Add(checkAbandonment(in.DB))
	r.Add(checkMemoryReview(in.DB))
	r.Add(checkHosts())
	r.Add(checkDuplicateRegistration(in.Hosts))
	if c, ok := checkCachedRegistration(in.Hosts); ok {
		r.Add(c)
	} else if c, ok := checkMissingRegistration(in.Hosts); ok {
		r.Add(c)
	}
	if c, ok := checkOtherVault(in.Hosts, vault.Recorded()); ok {
		r.Add(c)
	}
	if c, ok := checkOldHostPin(in.Hosts); ok {
		r.Add(c)
	}
	if c, ok := checkPlugin(in.Version); ok {
		r.Add(c)
	}
	if in.Self != "" {
		if c := CheckOtherInstall(in.Self, in.Version); c.Name != "" {
			r.Add(c)
		}
	}
	return r
}

// checkDurability answers the one question the other eleven checks never asked:
// is there anything the cache holds that the vault does not?
//
// The first principle of this project is that the vault is the truth and
// .logos/index.db is a cache you may delete at any time. A write that reached
// the cache and failed to reach the vault breaks that, and it used to break it
// invisibly: the call that failed said so, honestly, to a process that then
// exited, and from that moment a stranded row read exactly like a durable one.
// doctor gave a vault in that state a clean bill of health, and the next
// `logos index` — the command every document here calls safe — deleted the row.
//
// Failed rather than Warn. This is not a backlog waiting on the user; it is
// data that one `rm -rf .logos` destroys, and the fix is a command.
func checkDurability(db *sql.DB) Check {
	c := Check{Name: "durability"}
	if db == nil {
		c.State, c.Detail = Unknown, "no index open"
		return c
	}
	// The two halves are asked separately and degrade separately. A store that
	// predates the column answers neither; a database opened without the session
	// tables — every caller but `logos doctor`, which initialises them first —
	// answers only the first. Letting the missing half veto the other would hide
	// a real memory at risk behind a table nobody in this vault had used yet.
	mems, merr := memory.Unflushed(db)
	notes, nerr := session.Unflushed(db)
	if merr != nil && nerr != nil {
		// Saying "ok" here would be the same silence-as-success this package
		// exists to refuse.
		c.State, c.Detail = Unknown, "this index does not record which writes reached the vault"
		return c
	}
	if merr != nil {
		mems = 0
	}
	if nerr != nil {
		notes = 0
	}
	if mems+notes == 0 {
		c.State, c.Detail = OK, "everything in the index is in the vault"
		if merr != nil || nerr != nil {
			// Half a measurement, reported as half a measurement. "Everything"
			// would be a claim about rows this call never looked at.
			c.Detail = "every memory in the index is in the vault"
			if merr != nil {
				c.Detail = "every working note in the index is in the vault"
			}
		}
		return c
	}
	var parts []string
	if mems > 0 {
		parts = append(parts, fmt.Sprintf("%d memor%s", mems, plural(mems)))
	}
	if notes > 0 {
		parts = append(parts, fmt.Sprintf("%d working %s", notes, pluralWord(notes, "note")))
	}
	c.State = Failed
	c.Detail = strings.Join(parts, " and ") + " reached the index but not the vault"
	// index, not a repair command, because `logos index` is what writes them
	// out — and naming it here is also what tells the user it is safe to run.
	c.Fix = "check the vault is writable, then run `logos index` to write them out"
	return c
}

func checkNotes(dir string, db *sql.DB) Check {
	c := Check{Name: "notes"}
	if db == nil {
		c.State, c.Detail = Unknown, "no index open, so the note count could not be read"
		c.Fix = "run `logos index`"
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
		// What to do depends on which of the two situations this is, and the
		// vault answers it. Telling a user whose vault is full of checkpoints
		// and memories to "add markdown to the vault" is advice for a problem
		// they do not have, about a directory they never open, and it is the
		// line that makes an MCP-only vault look broken.
		if newest, err := newestMarkdown(dir); err == nil && !newest.IsZero() {
			c.Fix = "run `logos index` to make the vault searchable as prose"
		} else {
			c.Fix = "add markdown to the vault, then run `logos index`"
		}
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
// the agent still quotes the old one", which otherwise looks like logos being
// wrong rather than logos being behind.
func checkFreshness(dir string, db *sql.DB) Check {
	c := Check{Name: "index"}
	if db == nil {
		c.State, c.Detail = Unknown, "no index open"
		c.Fix = "run `logos index`"
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
			// Never indexed, which is not the same thing as broken — and this
			// is the ordinary state of the vault the product actually aims at.
			// A user who installs the plugin and talks to their agent creates a
			// vault entirely over MCP: every checkpoint and memory is written
			// to disk, every tool reads them back, and no `logos` command is
			// ever run, so last_sync is unset and the document table is empty.
			// Reporting FAILED there told a user with a perfectly healthy vault
			// that something was broken, exited 1, and blamed the half of the
			// system they had never touched.
			//
			// Nothing is lost in this state and nothing is diverging; prose
			// search simply has not been built yet. Warn is the tier for
			// exactly that — working, with a chore outstanding — and it does
			// not set the exit code. Real divergence is checkDurability's
			// question, and it is a Failure there.
			c.State = Warn
			c.Detail = "the vault's markdown has not been indexed for prose search yet"
		} else {
			c.State, c.Detail = Unknown, "cannot tell when the index was last built"
		}
		c.Fix = "run `logos index`"
		return c
	}
	lag := newest.Sub(time.Unix(synced.Int64, 0))
	if lag > staleBy {
		c.State = Failed
		c.Detail = fmt.Sprintf("stale — newest note is %s ahead of the index", roughly(lag))
		c.Fix = "run `logos index`"
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
		c.Fix = "start Ollama and run `logos index` for semantic search"
	case embedded < notes:
		c.State = OK
		c.Detail = fmt.Sprintf("%d of %d notes", embedded, notes)
		c.Fix = "run `logos index` to embed the rest"
	default:
		c.State, c.Detail = OK, fmt.Sprintf("all %d notes", notes)
	}
	return c
}

func checkRuntime(rt *provider.Provider, configured, model string) Check {
	c := Check{Name: "model runtime"}
	if strings.EqualFold(strings.TrimSpace(configured), "off") {
		// Asked for, not missing: there is nothing to start and nothing to
		// install, only the setting to unset.
		c.State = OK
		c.Detail = "none — LOGOS_RUNTIME=off; memories and notes are matched by keyword, not meaning"
		c.Fix = "unset LOGOS_RUNTIME to use a runtime on this machine"
		return c
	}
	if rt == nil && configured != "" {
		// Failed, unlike having none: the user named this runtime, the server
		// will not fall back to localhost, and "install Ollama" would be
		// advice about a runtime they never asked logos to use.
		c.State = Failed
		c.Detail = "none — LOGOS_RUNTIME is " + configured + " and nothing answers there; memories and notes are matched by keyword, not meaning"
		c.Fix = "start the runtime at " + configured + ", or unset LOGOS_RUNTIME to use one on this machine"
		return c
	}
	if rt == nil {
		// Optional by design, so this is not Failed. Under the coding-agent
		// framing the host's own model does the generating and logos only ever
		// wanted embeddings.
		c.State = OK
		c.Detail = "none — continuity is unaffected; memories and notes are matched by keyword, not meaning"
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
		c.Fix = "run `logos setup`"
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
		c.Fix = "ask an agent to checkpoint, or run `logos checkpoint <project>`"
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

// Ingest is optional — a machine with no other coding agents installed will
// discover nothing, and that is not a fault. So this check never fails: it
// reports which harnesses' transcripts are readable here and how many candidates
// are waiting in the queue, so a user who ran `logos ingest` and forgot is
// reminded (invariant 3) rather than left with silent pending state.
func checkIngest(vaultDir string) Check {
	c := Check{Name: "ingest"}

	var readable []string
	needTxcript := false
	for _, a := range transcript.Available() {
		if a.Found {
			readable = append(readable, fmt.Sprintf("%s (%d)", a.Harness, a.Sessions))
		} else if strings.Contains(a.Reason, "txcript") {
			needTxcript = true
		}
	}

	pending := 0
	if strings.TrimSpace(vaultDir) != "" {
		cs, _ := ingest.Pending(vaultDir)
		pending = len(cs)
	}

	c.State = OK
	switch {
	case len(readable) == 0:
		c.Detail = "no coding-agent transcripts found on this machine"
	default:
		c.Detail = "readable: " + strings.Join(readable, ", ")
	}
	if pending > 0 {
		// The same backlog the other two chore checks report, graded the same.
		c.State = Warn
		c.Detail += fmt.Sprintf("; %d candidate(s) pending review", pending)
		c.Fix = "run `logos ingest review`"
	}
	if needTxcript && c.Fix == "" {
		c.Fix = "install txcript to read more harness formats"
	}
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
	// Warn, not Failed. Notes held by a session that stopped is exactly what
	// this check exists to surface, but it is a chore — nothing is broken, and
	// there is no state the user can reach where it stays empty for long. As
	// Failed it made `logos doctor` exit 1 on every healthy install that had
	// ever lost a session, which is every install.
	c.State = Warn
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
	c.Fix = "run `logos sessions <project>` to see the notes, then checkpoint them or let the session go"
	return c
}

// checkMemoryReview is the PRODUCT RULE applied to quarantine: a feature that
// silently queues machine-proposed memories and never says so is no better
// than the unreviewed writes it replaced — the queue just fills up somewhere
// nobody looks. This is what makes the backlog visible on every `logos
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
	c.State = Warn
	c.Detail = fmt.Sprintf("%d memor%s awaiting review", n, plural(n))
	c.Fix = "run `logos review` to accept or reject them"
	return c
}
