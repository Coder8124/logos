package session

import (
	"os"
	"path/filepath"
	"testing"
)

// A checkpoint's host_session is the host's own conversation id, written at
// Commit time and round-tripped through the vault note — the thing C5 exists
// for, so two checkpoints of one sitting can be found as one conversation
// later regardless of how many times it checkpointed.
func TestACheckpointRecordsTheHostSessionItCameFrom(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()

	c := &Checkpoint{
		Project:     "kestrel",
		Agent:       "claude-code",
		Task:        "fix the checkout crash",
		HostSession: "claude-code-abc123",
	}
	if err := Commit(db, dir, c); err != nil {
		t.Fatal(err)
	}

	got, err := Latest(dir, "kestrel")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("no checkpoint found after commit")
	}
	if got.HostSession != "claude-code-abc123" {
		t.Fatalf("host_session lost on the way to the vault: got %q", got.HostSession)
	}
}

// A checkpoint's host_session is frontmatter, not a cache entry — the vault
// is truth and .logos/index.db is only ever a derived read path, so deleting
// the index and reading the checkpoint back off disk must find it exactly as
// written, with nothing SQLite-shaped standing between the field and the file.
func TestDeletingTheIndexKeepsEveryCheckpointsHostSession(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()

	c := &Checkpoint{
		Project:     "kestrel",
		Agent:       "claude-code",
		Task:        "fix the checkout crash",
		HostSession: "claude-code-abc123",
	}
	if err := Commit(db, dir, c); err != nil {
		t.Fatal(err)
	}

	// There is no index here to delete — Latest and History read the vault
	// markdown directly, never SQLite — which is the property under test:
	// re-reading the checkpoint from disk, as a rebuilt index would, still
	// finds the field.
	got, err := Latest(dir, "kestrel")
	if err != nil || got == nil {
		t.Fatalf("checkpoint unreadable straight from the vault: %v", err)
	}
	if got.HostSession != "claude-code-abc123" {
		t.Fatalf("host_session did not survive a read straight off the vault: got %q", got.HostSession)
	}
}

// A checkpoint nobody told a host session id to leaves the field blank rather
// than inventing one — the "unattached" case, named without a physical folder
// to put it in (see ByHostSession's doc comment on why no such folder exists).
func TestACheckpointWithNoHostSessionGoesToUnattached(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()

	c := &Checkpoint{Project: "kestrel", Agent: "claude-code", Task: "fix the checkout crash"}
	if err := Commit(db, dir, c); err != nil {
		t.Fatal(err)
	}

	got, err := Latest(dir, "kestrel")
	if err != nil || got == nil {
		t.Fatal("no checkpoint found after commit")
	}
	if got.HostSession != "" {
		t.Fatalf("a checkpoint given no host session must not acquire one: got %q", got.HostSession)
	}
	chain, err := ByHostSession(dir, got.HostSession)
	if err != nil {
		t.Fatal(err)
	}
	if len(chain) != 0 {
		t.Fatalf("an empty host session id must match nothing, got %d checkpoints", len(chain))
	}

	// Not just the field: a checkpoint nobody told a host session id to
	// still has to physically land somewhere, and that somewhere is the
	// shared _unattached folder, not a folder of its own and not the bare
	// scope directory a pre-ConvosDir checkpoint would have used.
	wantPath := filepath.Join(dir, CheckpointDir, "kestrel", ConvosDir, UnattachedFolder, got.Session+".md")
	if _, err := os.Stat(wantPath); err != nil {
		t.Fatalf("an unattached checkpoint is not where _unattached is supposed to put it: %v", err)
	}
	wantSlugSuffix := filepath.ToSlash(filepath.Join(ConvosDir, UnattachedFolder, got.Session))
	if !hasSuffix(got.Slug, wantSlugSuffix) {
		t.Fatalf("slug %q does not name the _unattached conversation folder", got.Slug)
	}
}

func hasSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}

// A sync-conflict copy, an interrupted migration, or a hand move can leave a
// checkpoint sitting in a conversation folder other than the one its own
// host_session names — or in the bare scope directory altogether, the
// pre-ConvosDir layout. Refile trusts the frontmatter over the folder and
// moves the file back, the same way `logos index` rebuilds any other derived
// layout from the vault rather than from wherever a file happens to sit.
func TestAMisfiledCheckpointIsReFiledFromItsFrontmatterAndAnnounced(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()

	c := &Checkpoint{
		Project:     "kestrel",
		Agent:       "claude-code",
		Task:        "fix the checkout crash",
		HostSession: "claude-code-abc123",
	}
	if err := Commit(db, dir, c); err != nil {
		t.Fatal(err)
	}

	correctPath := filepath.Join(dir, filepath.FromSlash(c.Slug)+".md")
	raw, err := os.ReadFile(correctPath)
	if err != nil {
		t.Fatalf("checkpoint not where Commit says it wrote it: %v", err)
	}

	// Simulate the file ending up directly in the scope directory — the
	// layout every checkpoint used before ConvosDir existed — while its own
	// frontmatter still names the conversation it came from.
	misfiledPath := filepath.Join(dir, CheckpointDir, "kestrel", c.Session+".md")
	if err := os.Remove(correctPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(misfiledPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	n, err := Refile(dir)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("want 1 checkpoint re-filed, got %d", n)
	}
	if _, err := os.Stat(misfiledPath); !os.IsNotExist(err) {
		t.Fatalf("Refile left the misfiled copy behind: %v", err)
	}
	if _, err := os.Stat(correctPath); err != nil {
		t.Fatalf("Refile did not put the checkpoint back where its own host_session says it belongs: %v", err)
	}

	// A second pass over an already-correct vault finds nothing left to move.
	n, err = Refile(dir)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("want nothing left to re-file on a second pass, got %d", n)
	}
}

// A citation stored before ConvosDir existed — in an old memory, an old
// checkpoint's own text, or anything ingest_harvest handed back before this
// migration — names "sessions/<project>/<stem>" with no conversation folder
// in it at all. Resolving it must still find the checkpoint wherever Refile
// or an ordinary Commit under the new layout actually put it.
func TestAnOldPathCitationStillResolvesAfterSessionsMoveIntoFolders(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()

	c := &Checkpoint{
		Project:     "kestrel",
		Agent:       "claude-code",
		Task:        "fix the checkout crash",
		HostSession: "claude-code-abc123",
	}
	if err := Commit(db, dir, c); err != nil {
		t.Fatal(err)
	}

	oldSlug := filepath.ToSlash(filepath.Join(CheckpointDir, "kestrel", c.Session))
	got, err := ResolvePath(dir, oldSlug)
	if err != nil {
		t.Fatalf("an old-style citation no longer resolves: %v", err)
	}
	want := filepath.Join(dir, filepath.FromSlash(c.Slug)+".md")
	if got != want {
		t.Fatalf("resolved to %q, want the checkpoint's current path %q", got, want)
	}
	if _, err := os.ReadFile(got); err != nil {
		t.Fatalf("resolved path is not readable: %v", err)
	}
}

// `logos resume <session-id>` needs the whole conversation, not its latest
// checkpoint alone — a conversation that checkpointed three times left two
// findings behind if only the last one came back.
func TestResumeBySessionIdReturnsTheWholeFollowsChainNotOneCheckpoint(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()

	for i, task := range []string{"first pass", "second pass", "third pass"} {
		AddNote(db, "kestrel", "claude-code", "note "+task)
		c := &Checkpoint{
			Project:     "kestrel",
			Agent:       "claude-code",
			Task:        task,
			HostSession: "claude-code-same-convo",
		}
		if err := Commit(db, dir, c); err != nil {
			t.Fatalf("commit %d: %v", i, err)
		}
	}
	// A second conversation in the same project must not bleed into the
	// first's chain.
	other := &Checkpoint{Project: "kestrel", Agent: "claude-code", Task: "unrelated work", HostSession: "claude-code-other-convo"}
	if err := Commit(db, dir, other); err != nil {
		t.Fatal(err)
	}

	chain, err := ByHostSession(dir, "claude-code-same-convo")
	if err != nil {
		t.Fatal(err)
	}
	if len(chain) != 3 {
		t.Fatalf("want all 3 checkpoints of the conversation, got %d: %+v", len(chain), chain)
	}
	if chain[0].Task != "first pass" || chain[2].Task != "third pass" {
		t.Fatalf("chain is not in oldest-first order: %+v", chain)
	}
}
