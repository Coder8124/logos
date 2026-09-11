package health

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/brain/internal/index"
	"github.com/Coder8124/brain/internal/session"
	"github.com/Coder8124/brain/internal/setup"
	"github.com/Coder8124/brain/internal/vault"

	_ "modernc.org/sqlite"
)

// The entire reason this package exists: a check that could not run must say so
// rather than passing. The old doctor reported on runtimes and tiers only, so a
// vault that did not exist and an index a week stale both read as healthy.

func find(t *testing.T, r Report, name string) Check {
	t.Helper()
	for _, c := range r.Checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no check named %q in report", name)
	return Check{}
}

func TestMissingVaultFailsAndDependentChecksAreUnknown(t *testing.T) {
	r := Run(Input{Vault: filepath.Join(t.TempDir(), "nope")})

	if got := find(t, r, "vault").State; got != Failed {
		t.Errorf("vault state = %q, want %q", got, Failed)
	}
	// The important half: with no index open, these must not claim to be fine.
	for _, name := range []string{"notes", "index", "embeddings"} {
		if got := find(t, r, name).State; got != Unknown {
			t.Errorf("%s state = %q, want %q — an unchecked thing is not a healthy thing",
				name, got, Unknown)
		}
	}
	if r.Healthy() {
		t.Error("a report with a failed vault should not be healthy")
	}
}

func TestUnwritableVaultIsCaught(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write anywhere")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })

	// Readable is not enough — checkpoints are writes, and discovering that at
	// handoff time is discovering it too late.
	if got := find(t, Run(Input{Vault: dir}), "vault").State; got != Failed {
		t.Errorf("vault state = %q on a read-only directory, want %q", got, Failed)
	}
}

func TestStaleIndexIsReported(t *testing.T) {
	dir := t.TempDir()
	ix, err := index.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()

	// Markdown in the vault, nothing in the index: the "I edited a note and the
	// agent still quotes the old one" case, which otherwise looks like brain
	// being wrong rather than brain being behind.
	if err := os.WriteFile(filepath.Join(dir, "note.md"), []byte("# a note\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	c := find(t, Run(Input{Vault: dir, DB: ix.DB}), "index")
	if c.State != Failed {
		t.Errorf("index state = %q with unindexed markdown, want %q", c.State, Failed)
	}
	if c.Fix == "" {
		t.Error("a failed index check should say what to do about it")
	}
}

// No model runtime is a supported configuration, not a fault. Reporting it as
// failed would send someone to fix something that is working as designed.
func TestNoRuntimeIsNotAFailure(t *testing.T) {
	dir := t.TempDir()
	r := Run(Input{Vault: dir, EmbedModel: "nomic-embed-text"})

	c := find(t, r, "model runtime")
	if c.State == Failed {
		t.Error("a missing runtime must not be reported as a failure")
	}
	if c.Fix == "" {
		t.Error("it should still say what installing one would buy")
	}
}

// Checkpoints live at sessions/<project>/<id>.md. Listing only the top level of
// sessions/ finds nothing and reports "no checkpoints yet" on a vault full of
// them — which would make the one check that observes continuity useless
// exactly when it matters.
func TestContinuityFindsNestedCheckpoints(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "sessions", "kestrel")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nproject: kestrel\nagent: codex\n---\n\nruled out injection moulding\n"
	if err := os.WriteFile(filepath.Join(nested, "20260830-120000-codex.md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	c := find(t, Run(Input{Vault: dir}), "continuity")
	if c.State != OK {
		t.Fatalf("continuity state = %q, want %q", c.State, OK)
	}
	for _, want := range []string{"kestrel", "codex"} {
		if !strings.Contains(c.Detail, want) {
			t.Errorf("continuity detail %q does not name %q", c.Detail, want)
		}
	}
}

// checkContinuity can be all-clear — a recent checkpoint exists — while a
// second, different session died mid-task and never made it that far. The two
// checks have to disagree in that case, or the second session's work stays
// invisible exactly the way this feature exists to prevent.
func TestAbandonedSessionsAreReported(t *testing.T) {
	dir := t.TempDir()
	ix, err := index.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	if err := session.Init(ix.DB); err != nil {
		t.Fatal(err)
	}

	old := time.Now().Add(-2 * session.AbandonAfter).Unix()
	if _, err := session.AddNoteAt(ix.DB, "kestrel", "codex", "found the bad batch", old); err != nil {
		t.Fatal(err)
	}

	c := find(t, Run(Input{Vault: dir, DB: ix.DB}), "abandoned sessions")
	if c.State != Failed {
		t.Fatalf("abandoned sessions state = %q, want %q", c.State, Failed)
	}
	if !strings.Contains(c.Detail, "kestrel") || !strings.Contains(c.Detail, "codex") {
		t.Errorf("detail %q does not name the abandoned session", c.Detail)
	}
	if c.Fix == "" {
		t.Error("a failed abandonment check should say what to do about it")
	}
}

func TestNoAbandonedSessionsIsOK(t *testing.T) {
	dir := t.TempDir()
	ix, err := index.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	if err := session.Init(ix.DB); err != nil {
		t.Fatal(err)
	}
	if _, err := session.AddNote(ix.DB, "kestrel", "codex", "just started"); err != nil {
		t.Fatal(err)
	}

	c := find(t, Run(Input{Vault: dir, DB: ix.DB}), "abandoned sessions")
	if c.State != OK {
		t.Errorf("abandoned sessions state = %q on a fresh session, want %q", c.State, OK)
	}
}

func TestCountsAndHealthy(t *testing.T) {
	r := Report{Checks: []Check{
		{Name: "a", State: OK},
		{Name: "b", State: Unknown},
		{Name: "c", State: Unknown},
	}}
	ok, failed, unknown := r.Counts()
	if ok != 1 || failed != 0 || unknown != 2 {
		t.Errorf("counts = %d/%d/%d, want 1/0/2", ok, failed, unknown)
	}
	// Unknowns mean unverified, not unhealthy — a different sentence.
	if !r.Healthy() {
		t.Error("unknowns alone should not make a report unhealthy")
	}
}

// The integration probe writes a checkpoint under a throwaway project name and
// removes it again. It used to remove only the file, and a project is a
// directory — so every run left behind a project that had never checkpointed,
// in the report that exists to name exactly those.

func TestTheIntegrationProbeLeavesNoProjectBehind(t *testing.T) {
	vault := t.TempDir()
	project := "brain-selftest-4242"
	dir := filepath.Join(vault, session.CheckpointDir, project)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	note := filepath.Join(dir, "20260903-120000-brain-doctor.md")
	if err := os.WriteFile(note, []byte("selftest ruled this out"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := cleanUp(vault, project, note); err != nil {
		t.Fatalf("cleanUp: %v", err)
	}

	got, err := session.Projects(vault)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range got {
		if p == project {
			t.Fatalf("%s is still a project after the probe cleaned up; `brain continuity` will list it as never checkpointed forever", project)
		}
	}
}

// But not at the cost of somebody's notes. If anything else is in the
// directory, the probe says it could not clean up rather than deleting it.

func TestTheProbeWillNotDeleteADirectoryItDoesNotOwn(t *testing.T) {
	vault := t.TempDir()
	project := "brain-selftest-4243"
	dir := filepath.Join(vault, session.CheckpointDir, project)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	note := filepath.Join(dir, "20260903-120000-brain-doctor.md")
	if err := os.WriteFile(note, []byte("selftest"), 0o644); err != nil {
		t.Fatal(err)
	}
	theirs := filepath.Join(dir, "uncommitted.md")
	if err := os.WriteFile(theirs, []byte("real work"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := cleanUp(vault, project, note); err == nil {
		t.Fatal("cleanUp reported success while leaving the directory behind")
	}
	if _, err := os.Stat(theirs); err != nil {
		t.Fatalf("the probe deleted a note it did not write: %v", err)
	}
}

// The privacy check used to answer from the vault directory's mode alone, so a
// vault at 0700 holding an index.db at 0644 was reported as "readable only by
// you". That file is a full copy of every note, memory and checkpoint, and
// index.Open sets its mode advisorily — `_ = vault.PrivateSiblings(...)`, with
// doctor named in the comment as the thing that would catch a failure. It did
// not catch it.

func TestAPrivateVaultHoldingAWorldReadableIndexIsNotPrivate(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".brain"), 0o700); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(dir, ".brain", "index.db")
	if err := os.WriteFile(db, []byte("every note you have"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := checkPrivacy(dir)
	if c.State == OK {
		t.Fatalf("privacy says %q while .brain/index.db is 0644", c.Detail)
	}
	if !strings.Contains(c.Detail, "index.db") {
		t.Fatalf("privacy failed but did not name the exposed file: %q", c.Detail)
	}
}

func TestAVaultPrivateAllTheWayDownPasses(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".brain"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".brain", "index.db"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "memories"), 0o700); err != nil {
		t.Fatal(err)
	}

	if c := checkPrivacy(dir); c.State != OK {
		t.Fatalf("privacy = %v (%s), want ok", c.State, c.Detail)
	}
}

// "no checkpoints yet" is the line a brand-new working vault prints. Printing
// it for a vault that is not there — and printing it as OK, on a report where
// every other vault-backed check said unchecked — tells the user continuity is
// fine when nothing about it was read.
func TestContinuityDoesNotReportOKForAVaultThatIsNotThere(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-vault-here")

	c := checkContinuity(missing)

	if c.State == OK {
		t.Fatalf("continuity = ok (%q) over a vault that does not exist", c.Detail)
	}
	if !strings.Contains(c.Fix, "brain setup") {
		t.Errorf("continuity fix is %q, want it to name `brain setup`", c.Fix)
	}
}

// A vault that exists and has simply never been checkpointed is the ordinary
// first-run case, and it must keep saying so.
func TestContinuityStillReportsOKForANewButRealVault(t *testing.T) {
	if c := checkContinuity(t.TempDir()); c.State != OK {
		t.Fatalf("continuity = %v (%s) on a real empty vault, want ok", c.State, c.Detail)
	}
}

// An open session with no notes in it made doctor permanently red, and the
// remedy it offered — "see the notes, then checkpoint them" — had nothing to
// operate on. Nothing was recorded in that session, so nothing is at risk: it
// is a row the vault does not describe and `brain index` would not rebuild.
// Say it happened, do not call the vault broken over it.
func TestAnEmptySessionLeftOpenDoesNotFailTheHealthCheck(t *testing.T) {
	dir := t.TempDir()
	ix, err := index.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	if err := session.Init(ix.DB); err != nil {
		t.Fatal(err)
	}

	old := time.Now().Add(-2 * session.AbandonAfter).Unix()
	if _, err := ix.DB.Exec(
		`INSERT INTO sessions (id, project, agent, task, started) VALUES (?,?,?,?,?)`,
		"empty-one", "help", "cli", "", old); err != nil {
		t.Fatal(err)
	}

	c := find(t, Run(Input{Vault: dir, DB: ix.DB}), "abandoned sessions")
	if c.State != OK {
		t.Fatalf("an open session holding no work reports %q: %s", c.State, c.Detail)
	}
	// Reported, not swallowed — the count is the whole announcement.
	if !strings.Contains(c.Detail, "1 empty session") {
		t.Errorf("detail %q does not mention the empty session it saw", c.Detail)
	}
}

// The other half: an empty session must not mask a real one. A vault holding
// both should still fail, and should name only the session with work in it.
func TestAnEmptySessionDoesNotHideOneHoldingWork(t *testing.T) {
	dir := t.TempDir()
	ix, err := index.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	if err := session.Init(ix.DB); err != nil {
		t.Fatal(err)
	}

	old := time.Now().Add(-2 * session.AbandonAfter).Unix()
	if _, err := ix.DB.Exec(
		`INSERT INTO sessions (id, project, agent, task, started) VALUES (?,?,?,?,?)`,
		"empty-one", "help", "cli", "", old); err != nil {
		t.Fatal(err)
	}
	if _, err := session.AddNoteAt(ix.DB, "kestrel", "codex", "found the bad batch", old); err != nil {
		t.Fatal(err)
	}

	c := find(t, Run(Input{Vault: dir, DB: ix.DB}), "abandoned sessions")
	if c.State != Failed {
		t.Fatalf("a session holding work reports %q: %s", c.State, c.Detail)
	}
	if !strings.Contains(c.Detail, "kestrel") {
		t.Errorf("detail %q does not name the session that has work in it", c.Detail)
	}
	if strings.Contains(c.Detail, "help") {
		t.Errorf("detail %q counts the empty session as work that was dropped", c.Detail)
	}
}

// uncommitted.md is rewritten every time an agent records a working note, so it
// is nearly always the newest file under sessions/. Taking it for a checkpoint
// made this check report "last checkpoint 4 hours ago" for a vault whose last
// real checkpoint was days old — the continuity check answering the exact
// question it exists for, with its opposite.
func TestWorkingNotesAreNotMistakenForACheckpoint(t *testing.T) {
	dir := t.TempDir()
	proj := filepath.Join(dir, session.CheckpointDir, "brain")
	if err := os.MkdirAll(proj, 0o700); err != nil {
		t.Fatal(err)
	}

	// A real checkpoint, written five days ago.
	real := filepath.Join(proj, "20260101-120000-claude.md")
	if err := os.WriteFile(real, []byte("---\ntype: checkpoint\nproject: brain\nagent: claude\n---\nstopped here\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-5 * 24 * time.Hour)
	if err := os.Chtimes(real, stale, stale); err != nil {
		t.Fatal(err)
	}

	// Working notes, touched moments ago. Not a checkpoint.
	if err := os.WriteFile(filepath.Join(proj, session.NotesFile),
		[]byte("# uncommitted — brain\n\n- still going <!-- ts=1 agent=cli -->\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	c := find(t, Run(Input{Vault: dir}), "continuity")
	if strings.Contains(c.Detail, "minute") || strings.Contains(c.Detail, "moment") {
		t.Errorf("working notes were counted as the last checkpoint: %q", c.Detail)
	}
	if !strings.Contains(c.Detail, "day") {
		t.Errorf("detail %q does not report the five-day-old checkpoint that is actually the newest", c.Detail)
	}
	// And it must still name the checkpoint it did find, rather than the empty
	// frontmatter of the file it used to pick up.
	if !strings.Contains(c.Detail, "brain") || strings.Contains(c.Detail, "— ,") {
		t.Errorf("detail %q does not name the checkpoint's project", c.Detail)
	}
}

// A saved plan lives in sessions/<project>/plans/, a subdirectory of the same
// tree latestCheckpoint walks recursively. Its filename is prefixed "plan-",
// not a bare timestamp, specifically so it fails IsCheckpointFile's test —
// this proves that holds through the health check that actually depends on
// it, not just through a direct call to IsCheckpointFile.
func TestASavedPlanIsNotMistakenForACheckpoint(t *testing.T) {
	dir := t.TempDir()
	proj := filepath.Join(dir, session.CheckpointDir, "brain")
	if err := os.MkdirAll(proj, 0o700); err != nil {
		t.Fatal(err)
	}

	real := filepath.Join(proj, "20260101-120000-claude.md")
	if err := os.WriteFile(real, []byte("---\ntype: checkpoint\nproject: brain\nagent: claude\n---\nstopped here\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-5 * 24 * time.Hour)
	if err := os.Chtimes(real, stale, stale); err != nil {
		t.Fatal(err)
	}

	// A plan saved moments ago, nested under sessions/brain/plans/.
	if _, err := session.SavePlan(dir, session.Plan{
		Project: "brain",
		Agent:   "claude",
		Text:    "a freshly approved plan",
	}); err != nil {
		t.Fatal(err)
	}

	c := find(t, Run(Input{Vault: dir}), "continuity")
	if strings.Contains(c.Detail, "minute") || strings.Contains(c.Detail, "moment") {
		t.Errorf("the just-saved plan was counted as the last checkpoint: %q", c.Detail)
	}
	if !strings.Contains(c.Detail, "day") {
		t.Errorf("detail %q does not report the five-day-old checkpoint that is actually the newest", c.Detail)
	}
}

// A scratch vault is a documented way to exercise the CLI — CONTRIBUTING.md
// says `BRAIN_VAULT=$(mktemp -d)` in as many words. But `brain setup --vault
// <scratch>` also RECORDS that path in os.UserConfigDir()/brain/vault-path,
// which is the durable pointer every front end reads when BRAIN_VAULT is unset.
// So testing against a scratch vault silently repoints the user's real install
// at a temporary directory, and index.Open creates whatever it is pointed at —
// producing a healthy zero of everything.
//
// This happened: the author's pointer named /var/folders/.../T/tmp.AFhBsX8awu/vault
// for a day, and doctor called it "vault ok / notes: none indexed yet". A
// failure reported as success is the one outcome this package exists to prevent.
//
// The recorded pointer is what gets checked, not the resolved directory: an
// explicit BRAIN_VAULT is a deliberate choice scoped to one command, while the
// pointer outlives the session and reaches the desktop app.

func TestARecordedVaultInsideTheTempDirectoryIsReported(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("BRAIN_VAULT", "")

	scratch := filepath.Join(os.TempDir(), "brain-scratch-doctor-test")
	if err := os.MkdirAll(scratch, 0o700); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(scratch)
	if err := vault.Record(scratch); err != nil {
		t.Fatal(err)
	}

	c := checkVault(scratch)
	if c.State != Failed {
		t.Fatalf("vault check = %v (%q), want failed — the recorded pointer names a temporary directory", c.State, c.Detail)
	}
	if !strings.Contains(c.Detail, "temporary") {
		t.Fatalf("failed, but did not say why: %q", c.Detail)
	}
	if c.Fix == "" {
		t.Fatal("no fix offered for a vault the user cannot be expected to diagnose")
	}
}

// A real vault must not be dragged into this, and neither must a deliberate
// one-off BRAIN_VAULT pointed at a scratch directory.

func TestAnOrdinaryVaultWithNoRecordedPointerStillPasses(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("BRAIN_VAULT", "")

	dir := filepath.Join(home, "brain")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if c := checkVault(dir); c.State != OK {
		t.Fatalf("vault check = %v (%q), want ok", c.State, c.Detail)
	}
}

func TestAScratchVaultChosenForOneCommandIsNotAComplaint(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	scratch := filepath.Join(os.TempDir(), "brain-scratch-env-only")
	if err := os.MkdirAll(scratch, 0o700); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(scratch)
	// Chosen by environment, never recorded — the documented workflow.
	t.Setenv("BRAIN_VAULT", scratch)

	if c := checkVault(scratch); c.State != OK {
		t.Fatalf("vault check = %v (%q), want ok — BRAIN_VAULT is a deliberate one-off", c.State, c.Detail)
	}
}

// A host that lists two servers both invoking "mcp serve" is brain registered
// twice under one roof — the exact shape a plugin registration and a manual
// `brain mcp install` produce side by side, and the configuration that
// measurably breached the 10k-token ceiling in the memory architecture plan.
func TestDuplicateRegistrationIsCaught(t *testing.T) {
	hosts := []setup.Host{{
		Name:   "Claude Code",
		Detect: func() bool { return true },
		List: func() ([]setup.Registration, error) {
			return []setup.Registration{
				{Name: "brain", Command: "/usr/local/bin/brain mcp serve"},
				{Name: "plugin:logos:logos", Command: "/opt/plugins/logos/bin/mcp.sh mcp serve"},
			}, nil
		},
	}}
	c := checkDuplicateRegistration(hosts)
	if c.State != Failed {
		t.Fatalf("state = %v, want failed", c.State)
	}
	if !strings.Contains(c.Detail, "brain") || !strings.Contains(c.Detail, "plugin:logos:logos") {
		t.Errorf("detail does not name both entries: %q", c.Detail)
	}
}

// One registration per host is the ordinary, healthy case and must not be
// flagged just because the check now exists.
func TestASingleRegistrationPerHostIsFine(t *testing.T) {
	hosts := []setup.Host{{
		Name:   "Claude Code",
		Detect: func() bool { return true },
		List: func() ([]setup.Registration, error) {
			return []setup.Registration{{Name: "brain", Command: "/usr/local/bin/brain mcp serve"}}, nil
		},
	}}
	if c := checkDuplicateRegistration(hosts); c.State != OK {
		t.Errorf("state = %v (%q), want ok", c.State, c.Detail)
	}
}

// Two different hosts each running brain once is two separate applications
// doing the right thing, not one session paying twice.
func TestOneRegistrationOnEachOfTwoHostsIsNotADuplicate(t *testing.T) {
	one := func(name string) setup.Host {
		return setup.Host{
			Name:   name,
			Detect: func() bool { return true },
			List: func() ([]setup.Registration, error) {
				return []setup.Registration{{Name: "brain", Command: "/usr/local/bin/brain mcp serve"}}, nil
			},
		}
	}
	hosts := []setup.Host{one("Claude Desktop"), one("Cursor")}
	if c := checkDuplicateRegistration(hosts); c.State != OK {
		t.Errorf("state = %v (%q), want ok", c.State, c.Detail)
	}
}

// A host with nothing else registered besides brain, plus an unrelated MCP
// server, must not be flagged — the signature is "mcp serve" specifically,
// not "more than one entry".
func TestAnUnrelatedSecondServerIsNotMistakenForADuplicate(t *testing.T) {
	hosts := []setup.Host{{
		Name:   "Cursor",
		Detect: func() bool { return true },
		List: func() ([]setup.Registration, error) {
			return []setup.Registration{
				{Name: "brain", Command: "/usr/local/bin/brain mcp serve"},
				{Name: "some-other-server", Command: "npx some-other-mcp"},
			}, nil
		},
	}}
	if c := checkDuplicateRegistration(hosts); c.State != OK {
		t.Errorf("state = %v (%q), want ok", c.State, c.Detail)
	}
}

// A host that cannot be introspected at all — not installed, or installed
// but with no List — must report Unknown, never a false OK.
func TestNoIntrospectableHostReportsUnknownNotOK(t *testing.T) {
	hosts := []setup.Host{
		{Name: "Codex", Detect: func() bool { return true }}, // no List
		{Name: "Cursor", Detect: func() bool { return false }, List: func() ([]setup.Registration, error) {
			t.Fatal("must not call List on an undetected host")
			return nil, nil
		}},
	}
	c := checkDuplicateRegistration(hosts)
	if c.State != Unknown {
		t.Errorf("state = %v (%q), want unknown", c.State, c.Detail)
	}
}

// os.TempDir() answers $TMPDIR, which on macOS is a per-user directory under
// /var/folders. It is not the only system temporary directory, and it is not
// the one that actually bit: agent scratchpads and most scripts use /tmp, which
// is a symlink to /private/tmp and shares no prefix with $TMPDIR at all. So a
// vault recorded under /tmp passed the check that exists precisely to catch it,
// and `brain doctor` reported "ok" over a vault that a reboot deletes.
func TestAVaultRecordedUnderSlashTmpIsCaughtToo(t *testing.T) {
	for _, dir := range []string{"/tmp/some-vault", "/private/tmp/some-vault", "/var/tmp/some-vault"} {
		if !underTempDir(dir) {
			t.Errorf("%s is a temporary directory, and the health check does not think so", dir)
		}
	}
	// The guard must still not fire on a real home.
	for _, dir := range []string{"/Users/someone/brain", "/home/someone/brain", "/tmpfoo/brain"} {
		if underTempDir(dir) {
			t.Errorf("%s is a perfectly good vault, and the health check calls it temporary", dir)
		}
	}
}

// `brain setup --print-config` exists precisely for the MCP clients this
// check cannot see — anything that is not one of setup.Hosts()'s four. Both
// branches of the report must say so: the empty case, where it is the whole
// answer, and the some-detected case, where an agent on this machine talking
// to a fifth client still deserves to be told how.
func TestHostsCheckMentionsPrintConfigWhenNoneAreDetected(t *testing.T) {
	c := hostsCheck(nil)
	if c.State != Unknown {
		t.Errorf("state = %v, want %v when no known host is detected", c.State, Unknown)
	}
	if !strings.Contains(c.Fix, "--print-config") {
		t.Errorf("fix = %q, want it to mention `brain setup --print-config` for a host this check cannot see", c.Fix)
	}
}

func TestHostsCheckNamesWhatItSeesAndStillMentionsPrintConfig(t *testing.T) {
	c := hostsCheck([]string{"Claude Code"})
	if c.State != OK {
		t.Errorf("state = %v, want %v when a known host is detected", c.State, OK)
	}
	if !strings.Contains(c.Detail, "Claude Code") {
		t.Errorf("detail = %q, want it to name the detected host", c.Detail)
	}
	if !strings.Contains(c.Fix, "--print-config") {
		t.Errorf("fix = %q, want it to still mention `brain setup --print-config` for any client this check does not know", c.Fix)
	}
}
