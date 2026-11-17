package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func convoNames(t *testing.T, dir, project string) []string {
	t.Helper()
	es, err := os.ReadDir(filepath.Join(dir, CheckpointDir, project, ConvosDir))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out
}

// legacyBare renames a conversation's folder back to the bare id, which is
// what every vault written before folders carried an intent looks like.
func legacyBare(t *testing.T, dir, project, id string) {
	t.Helper()
	root := filepath.Join(dir, CheckpointDir, project, ConvosDir)
	for _, n := range convoNamesOf(t, root) {
		if convoFolderID(n) == id && n != id {
			if err := os.Rename(filepath.Join(root, n), filepath.Join(root, id)); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func convoNamesOf(t *testing.T, root string) []string {
	es, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out
}

func TestANewConversationsFolderCarriesWhatItWasFor(t *testing.T) {
	dir := t.TempDir()
	commitIn(t, dir, &Checkpoint{Project: "kestrel", Agent: "a", Task: "fix checkout", Intent: "Checkout crashes lose orders", HostSession: "eaafa69f-8db2"})
	got := convoNames(t, dir, "kestrel")
	if len(got) != 1 || got[0] != "fix-checkout--eaafa69f-8db2" {
		t.Fatalf("folders = %v", got)
	}
}

func TestALongIntentIsCutAtAWordBoundary(t *testing.T) {
	s := convoSlug(Checkpoint{Intent: "Make the nightly synchronisation of every vault survive a flaky network"})
	if len([]rune(s)) > 40 || strings.HasSuffix(s, "-") || !strings.HasPrefix(s, "make-the-nightly-synchronisation") {
		t.Fatalf("slug = %q", s)
	}
	if got := convoSlug(Checkpoint{Intent: "fix checkout"}); got != "fix-checkout" {
		t.Fatalf("intent fallback slug = %q", got)
	}
}

func TestASymbolInTheIntentDoesNotSpellItselfOutInTheFolderName(t *testing.T) {
	got := convoSlug(Checkpoint{Intent: "Tokens saved read ~0 for restores (cut)"})
	if got != "tokens-saved-read-0-for-restores-cut" {
		t.Fatalf("slug = %q", got)
	}
}

func TestASlashCommandTaskDoesNotNameTheFolder(t *testing.T) {
	if got := convoSlug(Checkpoint{Task: "/model opus", Intent: "Pick a faster model"}); got != "pick-a-faster-model" {
		t.Fatalf("slug = %q", got)
	}
	if got := convoSlug(Checkpoint{Task: "/model opus"}); got != "" {
		t.Fatalf("slash command alone named the folder %q", got)
	}
}

func TestALaterCheckpointWithAnotherIntentLandsInTheExistingFolder(t *testing.T) {
	dir := t.TempDir()
	commitIn(t, dir, &Checkpoint{Project: "kestrel", Agent: "a", Task: "First goal", HostSession: "sess-1"})
	commitIn(t, dir, &Checkpoint{Project: "kestrel", Agent: "a", Task: "A completely different goal", HostSession: "sess-1"})
	got := convoNames(t, dir, "kestrel")
	if len(got) != 1 || got[0] != "first-goal--sess-1" {
		t.Fatalf("folders = %v", got)
	}
	es, _ := CheckpointEntries(filepath.Join(dir, CheckpointDir, "kestrel"))
	if len(es) != 2 {
		t.Fatalf("entries = %v", es)
	}
}

func TestACheckpointWithNoIntentOrTaskKeepsTheBareID(t *testing.T) {
	dir := t.TempDir()
	commitIn(t, dir, &Checkpoint{Project: "kestrel", Agent: "a", State: "only state", HostSession: "sess-9"})
	if got := convoNames(t, dir, "kestrel"); len(got) != 1 || got[0] != "sess-9" {
		t.Fatalf("folders = %v", got)
	}
}

func TestTwoConversationsWithTheSameIntentStayTwoFolders(t *testing.T) {
	dir := t.TempDir()
	commitIn(t, dir, &Checkpoint{Project: "kestrel", Agent: "a", Task: "Fix checkout", HostSession: "sess-a"})
	commitIn(t, dir, &Checkpoint{Project: "kestrel", Agent: "a", Task: "Fix checkout", HostSession: "sess-b"})
	got := convoNames(t, dir, "kestrel")
	if len(got) != 2 {
		t.Fatalf("folders = %v", got)
	}
	if a, _ := ByHostSession(dir, "sess-a"); len(a) != 1 {
		t.Fatalf("sess-a chain = %d", len(a))
	}
}

func TestALegacyBareIDFolderIsStillFoundAndIsNamedOnce(t *testing.T) {
	dir := t.TempDir()
	commitIn(t, dir, &Checkpoint{Project: "kestrel", Agent: "a", Task: "Fix checkout", HostSession: "sess-1"})
	legacyBare(t, dir, "kestrel", "sess-1")
	root := filepath.Join(dir, CheckpointDir, "kestrel", ConvosDir, "sess-1")
	if err := os.WriteFile(filepath.Join(root, "transcript.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := FindTranscript(dir, "sess-1"); got != filepath.Join(root, "transcript.jsonl") {
		t.Fatalf("legacy folder's transcript = %q", got)
	}
	// A new checkpoint must join the legacy folder, not fork a second.
	commitIn(t, dir, &Checkpoint{Project: "kestrel", Agent: "a", Task: "Other", HostSession: "sess-1"})
	if got := convoNames(t, dir, "kestrel"); len(got) != 1 {
		t.Fatalf("folders = %v", got)
	}
	if moved, _, err := Refile(dir); err != nil || moved != 0 {
		t.Fatalf("Refile moved %d, %v; a bare folder is correctly filed", moved, err)
	}
	named, skipped, err := NameConvoFolders(dir)
	if err != nil || named != 1 || len(skipped) != 0 {
		t.Fatalf("named %d skipped %v err %v", named, skipped, err)
	}
	if got := convoNames(t, dir, "kestrel"); got[0] != "fix-checkout--sess-1" {
		t.Fatalf("folders = %v", got)
	}
	if got := FindTranscript(dir, "sess-1"); !strings.HasSuffix(got, "fix-checkout--sess-1/transcript.jsonl") {
		t.Fatalf("transcript did not move with its folder: %q", got)
	}
	if named, _, _ = NameConvoFolders(dir); named != 0 {
		t.Fatalf("a second pass renamed %d", named)
	}
}

func TestABareAndASluggedFolderForOneIDAreReportedNotMerged(t *testing.T) {
	dir := t.TempDir()
	commitIn(t, dir, &Checkpoint{Project: "kestrel", Agent: "a", Task: "Fix checkout", HostSession: "sess-1"})
	root := filepath.Join(dir, CheckpointDir, "kestrel", ConvosDir)
	if err := os.MkdirAll(filepath.Join(root, "sess-1"), 0o700); err != nil {
		t.Fatal(err)
	}
	named, skipped, err := NameConvoFolders(dir)
	if err != nil || named != 0 || len(skipped) != 1 {
		t.Fatalf("named %d skipped %v err %v", named, skipped, err)
	}
	if got := convoFoldersFor(filepath.Join(dir, CheckpointDir, "kestrel"), "sess-1"); got[0] != "fix-checkout--sess-1" {
		t.Fatalf("lookup order = %v", got)
	}
}
