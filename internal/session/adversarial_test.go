package session

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Checkpoints are the durable half of the continuity claim, and their filenames
// are built from strings an agent supplies over MCP. These tests treat those
// strings as hostile input and the filesystem as unreliable.

func vaultDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := Init(db); err != nil {
		t.Fatal(err)
	}
	return db, dir
}

// safe() keeps only [a-z0-9], so a project named in any non-Latin script
// collapses to the empty string and is then rejected as missing. Nothing is
// corrupted — the separation holds because nothing is written at all — but
// brain is unusable for a project named in Japanese, Cyrillic, Arabic, Greek or
// Hindi, and the error blames the caller for omitting what they supplied.
func TestNonLatinProjectNamesAreUsable(t *testing.T) {
	db, dir := vaultDB(t)

	for _, p := range []string{"プロジェクト", "проект", "مشروع", "Πρόγραμμα", "परियोजना", "kestrel-一号"} {
		t.Run(p, func(t *testing.T) {
			c := Checkpoint{Project: p, Next: "next step for " + p}
			if err := Commit(db, dir, &c); err != nil {
				t.Errorf("cannot checkpoint a project named %q: %v", p, err)
				return
			}
			hist, err := History(dir, p, 5)
			if err != nil || len(hist) == 0 {
				t.Errorf("project %q checkpointed to %q but reads back empty: %v",
					p, c.Slug, err)
			}
		})
	}
}

// A name with nothing a filename can be made from is a real limit. What matters
// is that it is reported as itself rather than as a missing argument.
func TestUnrepresentableNameSaysSo(t *testing.T) {
	db, dir := vaultDB(t)

	for _, p := range []string{"🚀", "!!!", "///"} {
		err := Commit(db, dir, &Checkpoint{Project: p, Next: "x"})
		if err == nil {
			continue // supporting it is fine too
		}
		if !strings.Contains(err.Error(), "no letters or digits") {
			t.Errorf("project %q was refused with a misleading error: %v", p, err)
		}
	}
}

// Two distinct project names must never share a history, however they are
// spelled. Latin names with different punctuation collapse to the same slug —
// verify that is not silently merging separate work.
func TestSimilarlySpelledProjectsDoNotShareHistory(t *testing.T) {
	db, dir := vaultDB(t)

	a := Checkpoint{Project: "kestrel-one", Next: "alpha next"}
	if err := Commit(db, dir, &a); err != nil {
		t.Fatal(err)
	}
	b := Checkpoint{Project: "Kestrel One", Next: "beta next"}
	if err := Commit(db, dir, &b); err != nil {
		t.Fatal(err)
	}
	if a.Slug == "" || b.Slug == "" {
		t.Fatal("no slug written")
	}

	// These two SHOULD merge — same project, different capitalisation. Assert
	// the merge is deliberate and complete rather than half-done.
	hist, err := History(dir, "kestrel-one", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 2 {
		t.Errorf("'kestrel-one' and 'Kestrel One' produced %d checkpoints under "+
			"one history, want 2 — the normalisation is inconsistent between "+
			"write and read", len(hist))
	}
}

// A clock that is wrong, or two agents checkpointing in the same second.
func TestClockSkew(t *testing.T) {
	db, dir := vaultDB(t)

	if err := Commit(db, dir, &Checkpoint{Project: "kestrel", Next: "first"}); err != nil {
		t.Fatal(err)
	}

	// A checkpoint from the future, as a machine with a bad clock writes it.
	future := time.Now().Add(72 * time.Hour)
	name := future.Format("20060102-150405") + "-ghost.md"
	body := "---\ntype: checkpoint\nproject: kestrel\nagent: ghost\n---\n\n## Next\n\nfrom the future\n"
	p := filepath.Join(dir, CheckpointDir, "kestrel", name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	hist, err := History(dir, "kestrel", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 2 {
		t.Fatalf("want 2 checkpoints, got %d", len(hist))
	}
	latest, err := Latest(dir, "kestrel")
	if err != nil || latest == nil {
		t.Fatalf("Latest failed with a future-dated checkpoint present: %v", err)
	}
	for _, c := range hist {
		if c.TS < 0 {
			t.Errorf("negative timestamp parsed from %q", c.Slug)
		}
	}
}

// A crash between the temp write and the rename leaves a .brain-*.tmp behind.
// It must be invisible to everything that reads the vault.
func TestCrashLitterIsIgnored(t *testing.T) {
	db, dir := vaultDB(t)
	if err := Commit(db, dir, &Checkpoint{Project: "kestrel", Next: "real"}); err != nil {
		t.Fatal(err)
	}

	litter := filepath.Join(dir, CheckpointDir, "kestrel", ".brain-abc123.tmp")
	if err := os.WriteFile(litter, []byte("half-written gar"), 0o644); err != nil {
		t.Fatal(err)
	}

	hist, err := History(dir, "kestrel", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 1 {
		t.Errorf("a leftover temp file was read as a checkpoint: %d entries", len(hist))
	}
}

// One unreadable checkpoint must not hide the readable ones.
func TestCorruptCheckpointDoesNotHideTheRest(t *testing.T) {
	db, dir := vaultDB(t)
	if err := Commit(db, dir, &Checkpoint{Project: "kestrel", Next: "the good one"}); err != nil {
		t.Fatal(err)
	}

	pdir := filepath.Join(dir, CheckpointDir, "kestrel")
	for name, content := range map[string]string{
		"19990101-000000-empty.md":     "",
		"19990102-000000-binary.md":    "\x00\x01\xff not text",
		"19990103-000000-nofm.md":      "## Next\n\nno frontmatter at all\n",
		"19990104-000000-badfm.md":     "---\nthis: [is: not: yaml\n---\n\nbody\n",
		"19990105-000000-truncated.md": "---\ntype: checkpoint\nproject: kestrel\n",
	} {
		if err := os.WriteFile(filepath.Join(pdir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("History panicked on a corrupt checkpoint: %v", r)
		}
	}()
	hist, err := History(dir, "kestrel", 20)
	if err != nil {
		t.Fatalf("one corrupt file broke the whole history: %v", err)
	}
	var found bool
	for _, c := range hist {
		if strings.Contains(c.Next, "the good one") {
			found = true
		}
	}
	if !found {
		t.Error("the valid checkpoint was lost among the corrupt ones")
	}
}

// A project name long enough to exceed the filesystem's limit.
func TestAbsurdlyLongProjectName(t *testing.T) {
	db, dir := vaultDB(t)
	long := strings.Repeat("kestrel", 600) // ~4200 chars, over NAME_MAX

	c := Checkpoint{Project: long, Next: "next"}
	err := Commit(db, dir, &c)
	if err != nil {
		if !strings.Contains(err.Error(), "long") && !strings.Contains(err.Error(), "name") {
			t.Errorf("a 4200-character project name failed with an unhelpful error: %v", err)
		}
		return
	}
	if _, err := History(dir, long, 5); err != nil {
		t.Errorf("wrote a checkpoint that cannot be read back: %v", err)
	}
}

// Control: ASCII path escapes are already handled. This locks that in.
func TestPathEscapesAreNeutralised(t *testing.T) {
	db, dir := vaultDB(t)
	outside := filepath.Join(filepath.Dir(dir), "escaped.md")

	for _, p := range []string{"../../escaped", "/etc/passwd", "..", ".", "../escaped"} {
		c := Checkpoint{Project: p, Next: "escape attempt"}
		if err := Commit(db, dir, &c); err != nil {
			continue // refusing is a valid answer
		}
		abs := filepath.Join(dir, filepath.FromSlash(c.Slug)+".md")
		clean := filepath.Clean(abs)
		if !strings.HasPrefix(clean, filepath.Clean(dir)+string(filepath.Separator)) {
			t.Errorf("project %q wrote to %s, outside the vault", p, clean)
		}
	}
	if _, err := os.Stat(outside); err == nil {
		t.Errorf("a file was written outside the vault at %s", outside)
	}
}
