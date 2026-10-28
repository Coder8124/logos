package session

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// commitIn writes one checkpoint the ordinary way and returns where it landed.
func commitIn(t *testing.T, dir string, c *Checkpoint) string {
	t.Helper()
	if err := Commit(testDB(t), dir, c); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, filepath.FromSlash(c.Slug)+".md")
}

// flatten moves a committed checkpoint back to where every checkpoint before
// C5 sat, directly in its project's directory, and returns the new path.
func flatten(t *testing.T, dir, project, path string) string {
	t.Helper()
	flat := filepath.Join(dir, CheckpointDir, project, filepath.Base(path))
	if err := os.Rename(path, flat); err != nil {
		t.Fatal(err)
	}
	return flat
}

func TestResumingByNameFindsTheRightFolder(t *testing.T) {
	dir := t.TempDir()
	commitIn(t, dir, &Checkpoint{Project: "kestrel", Agent: "a", Task: "fix checkout", Intent: "Checkout crashes lose orders", HostSession: "sess-checkout"})
	commitIn(t, dir, &Checkpoint{Project: "kestrel", Agent: "a", Task: "speed up search", Intent: "Search takes four seconds", HostSession: "sess-search"})
	// A later checkpoint without its own intent still carries the task's name.
	commitIn(t, dir, &Checkpoint{Project: "kestrel", Agent: "a", Task: "fix checkout", State: "half done", HostSession: "sess-checkout"})

	convs, err := Conversations(dir, "kestrel")
	if err != nil {
		t.Fatal(err)
	}
	if len(convs) != 2 {
		t.Fatalf("want 2 conversations, got %d", len(convs))
	}
	got, _, err := FindConversation(convs, "LOSE ORDERS")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "sess-checkout" || len(got.Chain) != 2 || got.Latest.State != "half done" {
		t.Fatalf("name resolved to the wrong conversation: %s with %d checkpoints, latest state %q", got.ID, len(got.Chain), got.Latest.State)
	}
	// An exact id wins, even when it is also a substring of another's name.
	byID, _, err := FindConversation(convs, "sess-search")
	if err != nil || byID.ID != "sess-search" {
		t.Fatalf("exact id did not resolve: %v %v", byID, err)
	}
	out := RenderConversation("kestrel", *got)
	if !strings.Contains(out, "half done") || strings.Contains(out, "Search takes four seconds") {
		t.Fatalf("the narrowed resume is not this conversation alone:\n%s", out)
	}
}

func TestAnAmbiguousNameListsCandidatesInsteadOfGuessing(t *testing.T) {
	dir := t.TempDir()
	commitIn(t, dir, &Checkpoint{Project: "kestrel", Agent: "a", Task: "t1", Intent: "Fix the auth redirect", HostSession: "one"})
	commitIn(t, dir, &Checkpoint{Project: "kestrel", Agent: "a", Task: "t2", Intent: "Fix the auth token expiry", HostSession: "two"})
	convs, _ := Conversations(dir, "kestrel")

	got, candidates, err := FindConversation(convs, "auth")
	if err == nil || got != nil {
		t.Fatalf("an ambiguous name picked %v instead of refusing", got)
	}
	if len(candidates) != 2 {
		t.Fatalf("want both candidates listed, got %d", len(candidates))
	}
	for _, c := range candidates {
		if line := ConversationLine(c); !strings.Contains(line, c.ID) || !strings.Contains(line, c.Name) {
			t.Fatalf("candidate line lacks its id or intent: %q", line)
		}
	}
	if _, cands, err := FindConversation(convs, "billing"); err == nil || len(cands) != 0 {
		t.Fatalf("a name matching nothing must say so, got err=%v candidates=%d", err, len(cands))
	}
}

func TestAnUnmigratedProjectStillResumesByIdUnderTheOldLayout(t *testing.T) {
	dir := t.TempDir()
	// The real pre-C5 vault: flat files, most with no host_session at all.
	old := flatten(t, dir, "kestrel", commitIn(t, dir, &Checkpoint{Project: "kestrel", Agent: "a", Task: "old work", Intent: "Ship the legacy export"}))
	flatten(t, dir, "kestrel", commitIn(t, dir, &Checkpoint{Project: "kestrel", Agent: "a", Task: "hooked work", HostSession: "sess-h"}))
	if _, err := os.Stat(filepath.Join(dir, CheckpointDir, "kestrel", ConvosDir)); err == nil {
		// Empty folders Commit left behind are fine; checkpoints in them are not.
		os.RemoveAll(filepath.Join(dir, CheckpointDir, "kestrel", ConvosDir))
	}

	convs, err := Conversations(dir, "kestrel")
	if err != nil {
		t.Fatal(err)
	}
	stem := strings.TrimSuffix(filepath.Base(old), ".md")
	got, _, err := FindConversation(convs, stem)
	if err != nil || got.Latest.Task != "old work" || !got.Flat {
		t.Fatalf("a flat checkpoint's own id did not resume it: %v %v", got, err)
	}
	// The host session id it will be filed under after migration works now.
	got, _, err = FindConversation(convs, "sess-h")
	if err != nil || got.Latest.Task != "hooked work" {
		t.Fatalf("a flat checkpoint's host session did not resume it: %v %v", got, err)
	}
	if _, err := os.Stat(old); err != nil {
		t.Fatal("reading an unmigrated project moved its files")
	}
}

func TestACrashMidMigrationLeavesTheOldFlatFileReadable(t *testing.T) {
	dir := t.TempDir()
	flat := flatten(t, dir, "kestrel", commitIn(t, dir, &Checkpoint{Project: "kestrel", Agent: "a", Task: "keep me", HostSession: "sess-k"}))

	removeFlat = func(string) error { return errors.New("disk went away") }
	rep, err := MigrateFlat(dir, false)
	removeFlat = os.Remove
	if err != nil {
		t.Fatal(err)
	}
	if rep.Moved != 0 || len(rep.Failed) != 1 || !strings.Contains(rep.Failed[0], "disk went away") {
		t.Fatalf("a failed remove was not reported as a failure: %+v", rep)
	}
	if _, err := os.Stat(flat); err != nil {
		t.Fatalf("the old flat file is gone after a failed move: %v", err)
	}
	convs, _ := Conversations(dir, "kestrel")
	if len(convs) != 1 || len(convs[0].Chain) != 1 || convs[0].Latest.Task != "keep me" {
		t.Fatalf("a half-moved checkpoint should read as exactly one checkpoint, got %+v", convs)
	}

	rep, err = MigrateFlat(dir, false)
	if err != nil || rep.Moved != 1 || len(rep.Failed) != 0 {
		t.Fatalf("running again did not finish the move: %+v %v", rep, err)
	}
	if _, err := os.Stat(flat); !os.IsNotExist(err) {
		t.Fatal("the stale flat copy survived the rerun")
	}
	if _, err := os.Stat(filepath.Join(dir, CheckpointDir, "kestrel", ConvosDir, "sess-k", filepath.Base(flat))); err != nil {
		t.Fatalf("the checkpoint is not in its folder: %v", err)
	}
}

func TestMigratingSessionsCountsWhatItMovedAndKeepsACollidingFile(t *testing.T) {
	dir := t.TempDir()
	moved := flatten(t, dir, "kestrel", commitIn(t, dir, &Checkpoint{Project: "kestrel", Agent: "a", Task: "plain"}))
	inFolder := commitIn(t, dir, &Checkpoint{Project: "kestrel", Agent: "a", Task: "folder copy", HostSession: "sess-c"})
	// A different checkpoint under the same name, flat: neither may overwrite the other.
	clash := filepath.Join(dir, CheckpointDir, "kestrel", filepath.Base(inFolder))
	raw, _ := os.ReadFile(inFolder)
	if err := os.WriteFile(clash, []byte(strings.Replace(string(raw), "folder copy", "flat copy", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	torn := filepath.Join(dir, CheckpointDir, "kestrel", "20200101-000000-a.md")
	os.WriteFile(torn, nil, 0o600)

	dry, _ := MigrateFlat(dir, true)
	if dry.Moved != 1 {
		t.Fatalf("dry run counted %d", dry.Moved)
	}
	if _, err := os.Stat(moved); err != nil {
		t.Fatal("a dry run moved a file")
	}
	rep, err := MigrateFlat(dir, false)
	if err != nil || rep.Moved != 1 || len(rep.Skipped) != 2 || len(rep.Failed) != 0 {
		t.Fatalf("want 1 moved, 2 skipped (collision, torn), got %+v %v", rep, err)
	}
	for _, p := range []string{clash, inFolder, torn} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s should have been left alone: %v", p, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, CheckpointDir, "kestrel", ConvosDir, UnattachedFolder, filepath.Base(moved))); err != nil {
		t.Fatal("a checkpoint with no host session did not move to _unattached")
	}
}

// A conversation's own text is printed straight to a terminal, so an escape
// sequence in it must arrive as nothing, not as a command to the terminal.
func TestARenderedConversationCarriesNoTerminalEscapes(t *testing.T) {
	dir := t.TempDir()
	commitIn(t, dir, &Checkpoint{Project: "kestrel", Agent: "a", Task: "t", Next: "run it\x1b]0;owned\x07 now", HostSession: "s"})
	convs, _ := Conversations(dir, "kestrel")
	if out := RenderConversation("kestrel", convs[0]); strings.ContainsAny(out, "\x1b\x07") || !strings.Contains(out, "run it]0;owned now") {
		t.Fatalf("control characters reached the rendered conversation: %q", out)
	}
}

// A worktree and its main checkout name checkpoint files independently, so
// the same name in both is two checkpoints, not one interrupted move.
func TestTheSameFilenameInTwoWorktreesIsTwoCheckpoints(t *testing.T) {
	dir := t.TempDir()
	main := commitIn(t, dir, &Checkpoint{Project: "kestrel", Agent: "a", Task: "main side", HostSession: "s"})
	wt := filepath.Join(dir, CheckpointDir, "kestrel", "wt", ConvosDir, "s")
	os.MkdirAll(wt, 0o755)
	raw, _ := os.ReadFile(main)
	os.WriteFile(filepath.Join(wt, filepath.Base(main)), []byte(strings.Replace(string(raw), "main side", "worktree side", 1)), 0o600)
	convs, _ := Conversations(dir, "kestrel")
	if len(convs) != 1 || len(convs[0].Chain) != 2 {
		t.Fatalf("want one conversation of two checkpoints, got %+v", convs)
	}
}
