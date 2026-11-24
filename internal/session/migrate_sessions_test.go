package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// convoDirOf is the conversation folder a checkpoint file sits in.
func convoDirOf(path string) string { return filepath.Base(filepath.Dir(path)) }

// flatNoHost writes a checkpoint the way every one before host_session was:
// flat in its project, naming no conversation. It returns the path and the
// receipts key for it.
func flatNoHost(t *testing.T, dir string, c *Checkpoint) (string, string, time.Time) {
	t.Helper()
	path := flatten(t, dir, c.Project, commitIn(t, dir, c))
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	stem := strings.TrimSuffix(filepath.Base(path), ".md")
	return path, c.Project + "/" + stem, time.Unix(ParseCheckpoint(string(raw)).TS, 0)
}

func TestMigrateFilesAFlatCheckpointIntoTheConversationItsReceiptNames(t *testing.T) {
	dir := t.TempDir()
	path, key, at := flatNoHost(t, dir, &Checkpoint{Project: "kestrel", Agent: "a", Task: "fix checkout", Intent: "Orders are lost"})
	// The same conversation's later checkpoint, already in its folder: the
	// flat one joins it there, under the name that folder already has.
	later := commitIn(t, dir, &Checkpoint{Project: "kestrel", Agent: "a", Task: "fix checkout", State: "done", HostSession: "sess-r"})

	rep, err := MigrateFlat(dir, false, Receipts(map[string][]Receipt{key: {{HostSession: "sess-r", At: at.Add(2 * time.Second)}}}))
	if err != nil || rep.Moved != 1 || rep.Recovered != 1 || len(rep.Failed) != 0 {
		t.Fatalf("want 1 moved and recovered, got %+v %v", rep, err)
	}
	want := filepath.Join(filepath.Dir(later), filepath.Base(path))
	raw, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("not filed into its conversation's folder: %v", err)
	}
	if c := ParseCheckpoint(string(raw)); c.HostSession != "sess-r" || c.Intent != "Orders are lost" {
		t.Fatalf("frontmatter after the move: host %q intent %q", c.HostSession, c.Intent)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("the flat copy is still there")
	}
	convs, _ := Conversations(dir, "kestrel")
	if len(convs) != 1 || convs[0].ID != "sess-r" || len(convs[0].Chain) != 2 {
		t.Fatalf("want one conversation of two checkpoints, got %+v", convs)
	}
	// Run again: nothing left to do.
	again, err := MigrateFlat(dir, false, nil)
	if err != nil || again.Moved != 0 || again.Named != 0 {
		t.Fatalf("a second run did more work: %+v %v", again, err)
	}
}

func TestAReceiptQuotedLongAfterTheCheckpointDoesNotClaimIt(t *testing.T) {
	dir := t.TempDir()
	path, key, at := flatNoHost(t, dir, &Checkpoint{Project: "kestrel", Agent: "a", Task: "tidy logs"})
	rep, err := MigrateFlat(dir, false, Receipts(map[string][]Receipt{key: {{HostSession: "sess-later", At: at.Add(2 * time.Hour)}}}))
	if err != nil || rep.Recovered != 0 || rep.Unattributed != 1 {
		t.Fatalf("want it unattributed, got %+v %v", rep, err)
	}
	stem := strings.TrimSuffix(filepath.Base(path), ".md")
	own := filepath.Join(dir, CheckpointDir, "kestrel", ConvosDir, "tidy-logs--"+stem, filepath.Base(path))
	raw, err := os.ReadFile(own)
	if err != nil {
		t.Fatalf("not filed in a folder of its own: %v", err)
	}
	if ParseCheckpoint(string(raw)).HostSession != "" {
		t.Fatal("a host session was written that nothing established")
	}
}

func TestACheckpointTwoConversationsClaimStaysUnattributedAndIsReported(t *testing.T) {
	dir := t.TempDir()
	path, key, at := flatNoHost(t, dir, &Checkpoint{Project: "kestrel", Agent: "a", Task: "tidy logs"})
	rep, err := MigrateFlat(dir, false, Receipts(map[string][]Receipt{key: {
		{HostSession: "sess-1", At: at.Add(time.Second)},
		{HostSession: "sess-2", At: at.Add(2 * time.Second)},
	}}))
	// Reported once, as ambiguous: counting it unattributed as well would
	// make the totals add up to more checkpoints than were moved.
	if err != nil || rep.Recovered != 0 || rep.Unattributed != 0 || len(rep.Ambiguous) != 1 || !strings.Contains(rep.Ambiguous[0], filepath.Base(path)) {
		t.Fatalf("want it reported ambiguous, got %+v %v", rep, err)
	}
	convs, _ := Conversations(dir, "kestrel")
	if len(convs) != 1 || convs[0].Latest.HostSession != "" {
		t.Fatalf("a guess was written: %+v", convs)
	}
}

func TestMigrateSplitsUnattachedIntoOwnFolders(t *testing.T) {
	dir := t.TempDir()
	a := commitIn(t, dir, &Checkpoint{Project: "kestrel", Agent: "a", Task: "first thing", TS: 1_700_000_000})
	b := commitIn(t, dir, &Checkpoint{Project: "kestrel", Agent: "b", Task: "second thing", TS: 1_700_000_100})
	if convoDirOf(a) != UnattachedFolder || convoDirOf(b) != UnattachedFolder {
		t.Fatalf("setup: no-host checkpoints landed in %s and %s", convoDirOf(a), convoDirOf(b))
	}
	rep, err := MigrateFlat(dir, false, nil)
	if err != nil || rep.Moved != 2 || rep.Unattributed != 2 {
		t.Fatalf("want 2 moved into their own folders, got %+v %v", rep, err)
	}
	for _, p := range []string{a, b} {
		stem := strings.TrimSuffix(filepath.Base(p), ".md")
		matches, _ := filepath.Glob(filepath.Join(dir, CheckpointDir, "kestrel", ConvosDir, "*--"+stem, filepath.Base(p)))
		if len(matches) != 1 {
			t.Fatalf("%s is not in a folder of its own", filepath.Base(p))
		}
	}
	if _, err := os.Stat(filepath.Dir(a)); !os.IsNotExist(err) {
		t.Fatal("the emptied _unattached folder was left behind")
	}
	// Refiling afterwards must not pull them back into _unattached.
	if moved, _, err := Refile(dir); err != nil || moved != 0 {
		t.Fatalf("refile moved %d (%v) back out of their own folders", moved, err)
	}
}

func TestMigrateNamesABareConversationFolderByItsIntent(t *testing.T) {
	dir := t.TempDir()
	named := commitIn(t, dir, &Checkpoint{Project: "kestrel", Agent: "a", Task: "fix checkout", Intent: "Orders are lost", HostSession: "sess-old"})
	// The folder as 0.5.8 and earlier named it: the host id alone.
	bareDir := filepath.Join(filepath.Dir(filepath.Dir(named)), "sess-old")
	if err := os.Rename(filepath.Dir(named), bareDir); err != nil {
		t.Fatal(err)
	}

	dry, err := MigrateFlat(dir, true, nil)
	if err != nil || dry.Named != 1 {
		t.Fatalf("dry run: %+v %v", dry, err)
	}
	if _, err := os.Stat(bareDir); err != nil {
		t.Fatal("a dry run renamed a folder")
	}
	rep, err := MigrateFlat(dir, false, nil)
	if err != nil || rep.Named != 1 || rep.Moved != 0 {
		t.Fatalf("want 1 folder named and nothing moved, got %+v %v", rep, err)
	}
	if _, err := os.Stat(named); err != nil {
		t.Fatalf("the folder was not renamed to its intent: %v", err)
	}
	// A save into the conversation lands in the renamed folder.
	next := commitIn(t, dir, &Checkpoint{Project: "kestrel", Agent: "a", Task: "fix checkout", State: "s", HostSession: "sess-old"})
	if filepath.Dir(next) != filepath.Dir(named) {
		t.Fatalf("next save went to %s", filepath.Dir(next))
	}
}

// Doctor and the status line read a checkpoint file's modification time as
// when it was written; a migration that rewrote it as "now" would make the
// oldest checkpoint in the vault look like the latest one.
func TestMigratingACheckpointKeepsWhenItWasWritten(t *testing.T) {
	dir := t.TempDir()
	path, key, at := flatNoHost(t, dir, &Checkpoint{Project: "kestrel", Agent: "a", Task: "old work"})
	written := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	if err := os.Chtimes(path, written, written); err != nil {
		t.Fatal(err)
	}
	rep, err := MigrateFlat(dir, false, Receipts(map[string][]Receipt{key: {{HostSession: "sess-o", At: at}}}))
	if err != nil || rep.Moved != 1 {
		t.Fatalf("%+v %v", rep, err)
	}
	moved, _ := filepath.Glob(filepath.Join(dir, CheckpointDir, "kestrel", ConvosDir, "*--sess-o", filepath.Base(path)))
	if len(moved) != 1 {
		t.Fatal("not moved into its conversation")
	}
	info, err := os.Stat(moved[0])
	if err != nil || !info.ModTime().Equal(written) {
		t.Fatalf("the moved checkpoint reads as written %v, not %v", info.ModTime(), written)
	}
}

// A conversation whose transcript was saved before any checkpoint has a
// folder already, under its bare id. The migrated checkpoint must join it,
// not start a second folder for the same conversation beside it.
func TestMigrateJoinsAFolderHoldingOnlyATranscript(t *testing.T) {
	dir := t.TempDir()
	path, key, at := flatNoHost(t, dir, &Checkpoint{Project: "kestrel", Agent: "a", Task: "fix checkout"})
	bare := filepath.Join(dir, CheckpointDir, "kestrel", ConvosDir, "sess-t")
	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bare, "transcript.md"), []byte("talk\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := MigrateFlat(dir, false, Receipts(map[string][]Receipt{key: {{HostSession: "sess-t", At: at}}}))
	if err != nil || rep.Moved != 1 || len(rep.Failed) != 0 {
		t.Fatalf("%+v %v", rep, err)
	}
	folders, _ := os.ReadDir(filepath.Join(dir, CheckpointDir, "kestrel", ConvosDir))
	var ids []string
	for _, f := range folders {
		if convoFolderID(f.Name()) == "sess-t" {
			ids = append(ids, f.Name())
		}
	}
	if len(ids) != 1 {
		t.Fatalf("one conversation has %d folders: %v", len(ids), ids)
	}
	conv := filepath.Join(dir, CheckpointDir, "kestrel", ConvosDir, ids[0])
	for _, f := range []string{filepath.Base(path), "transcript.md"} {
		if _, err := os.Stat(filepath.Join(conv, f)); err != nil {
			t.Fatalf("%s is not in the conversation's folder: %v", f, err)
		}
	}
}

// A sync client's conflict copy is the same checkpoint as another device
// saw it. Left behind, it would keep the old flat place (or _unattached)
// alive with a stray that no conversation owns.
func TestSyncCopiesFollowAMigratedCheckpoint(t *testing.T) {
	dir := t.TempDir()
	a := commitIn(t, dir, &Checkpoint{Project: "kestrel", Agent: "a", Task: "first thing"})
	stem := strings.TrimSuffix(filepath.Base(a), ".md")
	copyName := stem + " (conflicted copy).md"
	raw, _ := os.ReadFile(a)
	if err := os.WriteFile(filepath.Join(filepath.Dir(a), copyName), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := MigrateFlat(dir, false, nil)
	if err != nil || rep.Moved != 1 || len(rep.Failed) != 0 || len(rep.Skipped) != 0 {
		t.Fatalf("%+v %v", rep, err)
	}
	moved, _ := filepath.Glob(filepath.Join(dir, CheckpointDir, "kestrel", ConvosDir, "*--"+stem, copyName))
	if len(moved) != 1 {
		t.Fatal("the sync copy did not follow its checkpoint")
	}
	if _, err := os.Stat(filepath.Dir(a)); !os.IsNotExist(err) {
		t.Fatalf("_unattached was left behind holding the copy: %v", err)
	}
}

// Some writers leave the key with no value. Adding a second host_session
// beside it makes the frontmatter undecodable, and the checkpoint reads as
// empty everywhere afterwards.
func TestAnEmptyHostSessionKeyIsFilledNotDuplicated(t *testing.T) {
	for _, empty := range []string{"host_session:", `host_session: ""`, "host_session: ''"} {
		raw := "---\nsession: \"x\"\n" + empty + "\nagent: a\n---\nbody host_session: no\n"
		got, ok := withHostSession([]byte(raw), "sess-1")
		if !ok || strings.Count(string(got), "host_session:") != 2 || !strings.Contains(string(got), "host_session: "+yamlStr("sess-1")+"\n") {
			t.Fatalf("%q became:\n%s", empty, got)
		}
		if c := ParseCheckpoint(string(got)); c.HostSession != "sess-1" {
			t.Fatalf("%q: the result does not decode to its host: %+v", empty, c)
		}
	}
}

// Reading every host transcript costs seconds on a machine with years of
// them; a vault whose checkpoints all name their conversation needs none.
func TestTheReceiptSourceIsNotReadWhenEveryCheckpointHasAHost(t *testing.T) {
	dir := t.TempDir()
	flatten(t, dir, "kestrel", commitIn(t, dir, &Checkpoint{Project: "kestrel", Agent: "a", Task: "fix checkout", HostSession: "sess-h"}))
	read := false
	rep, err := MigrateFlat(dir, false, func() (map[string][]Receipt, []string, error) {
		read = true
		return nil, nil, nil
	})
	if err != nil || rep.Moved != 1 {
		t.Fatalf("%+v %v", rep, err)
	}
	if read {
		t.Fatal("the host transcripts were read for a vault that needed none")
	}
}

// renameStem gives a flat checkpoint another stem, as one written by an older
// Logos or by hand may carry; dateOnly also drops its precise `checkpointed`
// instant so only the day-resolution first_seen is left.
func renameStem(t *testing.T, path, stem string, dateOnly bool) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if dateOnly {
		var keep []string
		for _, l := range strings.Split(string(raw), "\n") {
			if !strings.HasPrefix(l, "checkpointed:") {
				keep = append(keep, l)
			}
		}
		raw = []byte(strings.Join(keep, "\n"))
	}
	to := filepath.Join(filepath.Dir(path), stem+".md")
	if err := os.WriteFile(to, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	return to
}

func TestAMigratedCheckpointWithAMixedCaseStemStaysInItsFolderAfterRefile(t *testing.T) {
	dir := t.TempDir()
	path, _, _ := flatNoHost(t, dir, &Checkpoint{Project: "kestrel", Agent: "a", Task: "tidy logs"})
	path = renameStem(t, path, "20250101-120000-My_Handoff.v2", false)
	rep, err := MigrateFlat(dir, false, nil)
	if err != nil || rep.Moved != 1 || rep.Unattributed != 1 {
		t.Fatalf("%+v %v", rep, err)
	}
	moved, _, err := Refile(dir)
	if err != nil || moved != 0 {
		t.Fatalf("the next index moved %d migrated checkpoint(s) back out of their own folder (%v)", moved, err)
	}
	if m, _ := filepath.Glob(filepath.Join(dir, CheckpointDir, "kestrel", ConvosDir, "*", filepath.Base(path))); len(m) != 1 || strings.Contains(m[0], UnattachedFolder) {
		t.Fatalf("checkpoint is not in a folder of its own: %v", m)
	}
}

func TestACheckpointIsLeftForALaterRunWhenAHostTranscriptCouldNotBeRead(t *testing.T) {
	dir := t.TempDir()
	path, key, at := flatNoHost(t, dir, &Checkpoint{Project: "kestrel", Agent: "a", Task: "tidy logs"})
	unreadable := func() (map[string][]Receipt, []string, error) {
		return map[string][]Receipt{}, []string{"/x/locked.jsonl"}, nil
	}
	rep, err := MigrateFlat(dir, false, unreadable)
	if err != nil || rep.Moved != 0 || len(rep.Deferred) != 1 || len(rep.Unreadable) != 1 {
		t.Fatalf("want it deferred, got %+v %v", rep, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("a checkpoint that may have a conversation was filed for good")
	}
	// The transcript is readable on the rerun: the checkpoint finds its host.
	rep, err = MigrateFlat(dir, false, Receipts(map[string][]Receipt{key: {{HostSession: "sess-late", At: at}}}))
	if err != nil || rep.Recovered != 1 || len(rep.Deferred) != 0 {
		t.Fatalf("the rerun did not recover it: %+v %v", rep, err)
	}
}

func TestADateOnlyCheckpointIsMatchedToItsReceiptByTheTimeInItsStem(t *testing.T) {
	dir := t.TempDir()
	path, _, _ := flatNoHost(t, dir, &Checkpoint{Project: "kestrel", Agent: "a", Task: "tidy logs"})
	path = renameStem(t, path, "20250101-153045-claude-code", true)
	stamp, _ := time.ParseInLocation("20060102-150405", "20250101-153045", time.Local)
	rep, err := MigrateFlat(dir, false, Receipts(map[string][]Receipt{"kestrel/20250101-153045-claude-code": {{HostSession: "sess-old", At: stamp.Add(2 * time.Second)}}}))
	if err != nil || rep.Recovered != 1 {
		t.Fatalf("a date-only checkpoint's receipt was dropped by the midnight window: %+v %v", rep, err)
	}
	_ = path
}

func TestTheSameReceiptCopiedIntoAForkedTranscriptIsOneClaimant(t *testing.T) {
	at := time.Unix(1_700_000_000, 0)
	host, n := hostFromReceipts([]Receipt{{HostSession: "orig", At: at}, {HostSession: "fork", At: at}}, at.Unix())
	if host != "orig" || n != 1 {
		t.Fatalf("a copied receipt read as two conversations: %q %d", host, n)
	}
}

func TestAFolderThatCannotBeRenamedIsReportedAndTheRestAreStillNamed(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root renames anywhere")
	}
	dir := t.TempDir()
	var bare []string
	for _, proj := range []string{"alpha", "beta"} {
		p := commitIn(t, dir, &Checkpoint{Project: proj, Agent: "a", Task: "fix " + proj, Intent: "Because " + proj, HostSession: "sess-" + proj})
		b := filepath.Join(filepath.Dir(filepath.Dir(p)), "sess-"+proj)
		if err := os.Rename(filepath.Dir(p), b); err != nil {
			t.Fatal(err)
		}
		bare = append(bare, b)
	}
	locked := filepath.Dir(bare[0])
	if err := os.Chmod(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o700) })
	rep, err := MigrateFlat(dir, false, nil)
	if err != nil || len(rep.NameFailed) != 1 || rep.Named != 1 || len(rep.Failed) != 0 {
		t.Fatalf("want one naming failure reported apart from checkpoint failures and the other folder named: %+v %v", rep, err)
	}
}
