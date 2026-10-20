package session

import (
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
