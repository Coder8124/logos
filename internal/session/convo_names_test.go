package session

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

func convoNames(t *testing.T, dir, project string) []string {
	t.Helper()
	return convoNamesOf(t, filepath.Join(dir, CheckpointDir, project, ConvosDir))
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
	t.Helper()
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

func TestACitationTakenBeforeTheRenameStillResolvesAfterIt(t *testing.T) {
	dir := t.TempDir()
	path := commitIn(t, dir, &Checkpoint{Project: "kestrel", Agent: "a", Task: "Fix checkout", HostSession: "sess-2"})
	stem := strings.TrimSuffix(filepath.Base(path), ".md")
	bareCite := "sessions/kestrel/_convos/sess-2/" + stem
	legacyBare(t, dir, "kestrel", "sess-2")
	if _, _, err := NameConvoFolders(dir); err != nil {
		t.Fatal(err)
	}
	got, err := ResolvePath(dir, bareCite)
	if err != nil {
		t.Fatalf("a citation written before the rename no longer resolves: %v", err)
	}
	if !strings.Contains(got, "fix-checkout--sess-2") {
		t.Fatalf("resolved to %s", got)
	}
}

func TestASluggedCitationStillResolvesWhenItsFolderIsBare(t *testing.T) {
	dir := t.TempDir()
	path := commitIn(t, dir, &Checkpoint{Project: "kestrel", Agent: "a", Task: "Fix checkout", HostSession: "sess-2"})
	stem := strings.TrimSuffix(filepath.Base(path), ".md")
	cite := "sessions/kestrel/_convos/fix-checkout--sess-2/" + stem
	legacyBare(t, dir, "kestrel", "sess-2")
	got, err := ResolvePath(dir, cite)
	if err != nil || !strings.HasSuffix(got, "/sess-2/"+stem+".md") {
		t.Fatalf("resolved %q, %v", got, err)
	}
}

func TestTwoSluggedFoldersForOneIDAreReportedNotRenamed(t *testing.T) {
	dir := t.TempDir()
	commitIn(t, dir, &Checkpoint{Project: "kestrel", Agent: "a", Task: "Fix checkout", HostSession: "sess-1"})
	root := filepath.Join(dir, CheckpointDir, "kestrel", ConvosDir)
	if err := os.MkdirAll(filepath.Join(root, "other-name--sess-1"), 0o700); err != nil {
		t.Fatal(err)
	}
	named, skipped, err := NameConvoFolders(dir)
	if err != nil || named != 0 || len(skipped) != 1 || !strings.Contains(skipped[0], "sess-1") {
		t.Fatalf("named %d skipped %v err %v", named, skipped, err)
	}
}

func TestNamingFoldersWaitsForAConversationBeingFiled(t *testing.T) {
	dir := t.TempDir()
	commitIn(t, dir, &Checkpoint{Project: "kestrel", Agent: "a", Task: "Fix checkout", HostSession: "sess-1"})
	legacyBare(t, dir, "kestrel", "sess-1")
	g, err := lockConvoFolders(dir)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, _, err := NameConvoFolders(dir); err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-done:
		g.Unlock()
		t.Fatal("a rename went ahead while a checkpoint held the folder lock")
	case <-time.After(300 * time.Millisecond):
	}
	g.Unlock()
	<-done
	if got := convoNames(t, dir, "kestrel"); len(got) != 1 || got[0] != "fix-checkout--sess-1" {
		t.Fatalf("folders = %v", got)
	}
}

func TestAnAutoRecordBeingGrownIsNotHavingItsFolderMoved(t *testing.T) {
	dir := t.TempDir()
	commitIn(t, dir, &Checkpoint{Project: "kestrel", Agent: "a", Task: "Fix checkout", HostSession: "sess-1"})
	legacyBare(t, dir, "kestrel", "sess-1")
	g, err := lockAutoRecords(dir)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _, _ = NameConvoFolders(dir)
	}()
	select {
	case <-done:
		g.Unlock()
		t.Fatal("a folder was renamed while an auto record was being rewritten")
	case <-time.After(300 * time.Millisecond):
	}
	g.Unlock()
	<-done
}

func TestFirstCheckpointsOfOneConversationWithDifferentTasksMakeOneFolder(t *testing.T) {
	for round := range 8 {
		dir := t.TempDir()
		var wg sync.WaitGroup
		for i := range 6 {
			db := testDB(t) // one per writer, as separate processes have
			wg.Add(1)
			go func() {
				defer wg.Done()
				c := &Checkpoint{Project: "kestrel", Agent: "a", Task: "task number " + string(rune('a'+i)), HostSession: "sess-1", State: "s"}
				if err := Commit(db, dir, c); err != nil {
					t.Error(err)
				}
			}()
		}
		wg.Wait()
		if got := convoNames(t, dir, "kestrel"); len(got) != 1 {
			t.Fatalf("round %d: one conversation made %d folders: %v", round, len(got), got)
		}
	}
}

func TestAFolderNameNeverExceedsWhatTheFilesystemAccepts(t *testing.T) {
	id := strings.Repeat("a", 230)
	for _, task := range []string{strings.Repeat("字", 60), "a long task with many words " + strings.Repeat("word ", 30)} {
		got := convoName(convoSlug(Checkpoint{Task: task}), id)
		if len(got) > maxNameLen || convoFolderID(got) != id {
			t.Fatalf("name is %d bytes, id part %q", len(got), convoFolderID(got))
		}
		if !utf8.ValidString(got) {
			t.Fatalf("name %q is not valid UTF-8", got)
		}
	}
	if got := convoName("some-slug", strings.Repeat("b", 254)); got != strings.Repeat("b", 254) {
		t.Fatalf("a slug with no room was kept: %d bytes", len(got))
	}
	if got := convoName(convoSlug(Checkpoint{Task: strings.Repeat("字", 60)}), "sess-1"); len(got) > maxNameLen || !strings.HasSuffix(got, "--sess-1") {
		t.Fatalf("CJK name %q", got)
	}
}
