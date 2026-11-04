package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #368: the old rule dropped every symbol, so C++, C# and C were one folder and
// resume gave each the others' stopping place as its own.
func TestProjectsWhoseNamesDifferOnlyInSymbolsKeepSeparateHistories(t *testing.T) {
	db, dir := vaultDB(t)
	for _, p := range []string{"C++", "C#", "C", "a:b", "ab"} {
		if err := Commit(db, dir, &Checkpoint{Project: p, Next: "next for " + p}); err != nil {
			t.Fatalf("checkpoint %q: %v", p, err)
		}
	}
	for _, p := range []string{"C++", "C#", "C", "a:b", "ab"} {
		hist, err := History(dir, p, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(hist) != 1 || hist[0].Next != "next for "+p {
			var got []string
			for _, c := range hist {
				got = append(got, c.Next)
			}
			t.Errorf("history of %q = %q, want only its own checkpoint", p, got)
		}
	}
}

func TestSymbolsAreSpelledAsWords(t *testing.T) {
	for in, want := range map[string]string{
		"C++":       "c-plus-plus",
		"C#":        "c-sharp",
		"a:b":       "a-colon-b",
		"a: b":      "a-colon-b",
		"k@stre!":   "k-u-40-stre-u-21",
		"🚀":         "u-1f680",
		"F# / ML":   "f-sharp-ml",
		"x\x00y\ty": "xy-y",
	} {
		if got := safe(in); got != want {
			t.Errorf("safe(%q) = %q, want %q", in, got, want)
		}
	}
}

// A scope goes through safe more than once on its way to disk (Current, then
// Start). If a spelled name changed on the second pass, a checkpoint would be
// filed where no lookup looks.
func TestASpelledNameIsUnchangedBySpellingItAgain(t *testing.T) {
	for _, in := range []string{"C++", "C#", "a:b", "k@stre!", "🚀", "Kestrel One/feature X", "プロジェクト"} {
		once := safeScope(in)
		if twice := safeScope(once); twice != once {
			t.Errorf("safeScope(%q) = %q, but again gives %q", in, once, twice)
		}
	}
}

// Only folders the old rule merged may move: a name of letters, digits and
// `- _ . space /` is filed exactly where it always was.
func TestNamesWithoutSymbolsKeepTheirFolder(t *testing.T) {
	for _, in := range []string{"kestrel", "Kestrel One", "kestrel_one.v2", "kestrel/feature-x", "プロジェクト", "परियोजना", "проект 2"} {
		if got, old := safeScope(in), legacyScope(in); got != old {
			t.Errorf("safeScope(%q) = %q, but it was filed under %q", in, got, old)
		}
	}
}

// History written before #368 sits in the folder the old rule chose. Until the
// project has a checkpoint of its own it is still read from there, and the
// first checkpoint into the new folder is the moment to say it stops being.
func TestHistoryFiledUnderTheOldNameIsReadUntilTheProjectHasItsOwn(t *testing.T) {
	db, dir := vaultDB(t)
	// What 0.5.6 wrote for "C++": the same files, under sessions/c.
	if err := Commit(db, dir, &Checkpoint{Project: "c", Next: "old step"}); err != nil {
		t.Fatal(err)
	}
	if got := Stranded(dir, "C++"); got != "c" {
		t.Fatalf("Stranded = %q, want c", got)
	}
	hist, err := History(dir, "C++", 10)
	if err != nil || len(hist) != 1 || hist[0].Next != "old step" {
		t.Fatalf("C++ did not read its pre-#368 history: %+v %v", hist, err)
	}
	if !strings.HasPrefix(hist[0].Slug, CheckpointDir+"/c/") {
		t.Errorf("slug %q does not name the folder it was read from", hist[0].Slug)
	}
	if !strings.Contains(StrandedNotice("C++", "c"), "logos project rename c c-plus-plus --merge") {
		t.Errorf("notice does not name the rename: %s", StrandedNotice("C++", "c"))
	}
	if err := Commit(db, dir, &Checkpoint{Project: "C++", Next: "new step"}); err != nil {
		t.Fatal(err)
	}
	if got := Stranded(dir, "C++"); got != "" {
		t.Errorf("Stranded = %q after the project has its own folder", got)
	}
	// Plain names are never sent anywhere else.
	if got := Stranded(dir, "c"); got != "" {
		t.Errorf("Stranded(c) = %q", got)
	}
}

// The session-end hook writes an auto record, and nothing it prints is read.
// The first one in a project's new folder ended the fallback, so every earlier
// checkpoint left resume without a word; resume has to say it until an agent
// checkpoints, which says it too.
func TestAnAutoRecordDoesNotQuietlyEndTheEarlierHistory(t *testing.T) {
	db, dir := vaultDB(t)
	if err := Commit(db, dir, &Checkpoint{Project: "c", Next: "old step"}); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteAuto(dir, Checkpoint{Project: "C++", Task: "edited files"}); err != nil {
		t.Fatal(err)
	}
	if got := Split(dir, "C++"); got != "c" {
		t.Fatalf("Split = %q after an auto record took over, want c", got)
	}
	if err := Commit(db, dir, &Checkpoint{Project: "C++", Next: "new step"}); err != nil {
		t.Fatal(err)
	}
	if got := Split(dir, "C++"); got != "" {
		t.Errorf("Split = %q after an agent checkpointed, which says it itself", got)
	}
}

// Normalised before the lookup, "C++" became c-plus-plus and no longer found
// sessions/c, so the auto record lost its link to the checkpoint before it.
func TestAnAutoRecordFollowsTheCheckpointFiledUnderTheOldName(t *testing.T) {
	db, dir := vaultDB(t)
	prev := &Checkpoint{Project: "c", Next: "old step"}
	if err := Commit(db, dir, prev); err != nil {
		t.Fatal(err)
	}
	c, err := WriteAuto(dir, Checkpoint{Project: "C++", Task: "edited files"})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(c.Slug)+".md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "pred: follows") {
		t.Errorf("auto record follows nothing, though %s is before it:\n%s", prev.Slug, raw)
	}
}

// Nothing in the old folder says which project wrote it, so a new "C#" in a
// vault with "C" reads C's history the same way; the notice must not call it
// C#'s.
func TestTheNoticeDoesNotSayTheOldFolderIsThisProjects(t *testing.T) {
	if n := StrandedNotice("C#", "c"); !strings.Contains(n, "may be another project's") {
		t.Errorf("notice claims the history: %s", n)
	}
}

// rename takes project names and refuses a separator; a worktree is moved with
// its project.
func TestAWorktreeIsToldToRenameItsProject(t *testing.T) {
	for _, n := range []string{StrandedNotice("C++/feat", "c/feat"), SplitNotice("C++/feat", "c/feat")} {
		if !strings.Contains(n, "logos project rename c c-plus-plus --merge") {
			t.Errorf("notice names a rename that is refused: %s", n)
		}
	}
}

// Session ids and agent names were never merged with each other, so spelling
// their symbols only moved a conversation's folder, or an agent's files,
// partway through.
func TestHostSessionIdsAndAgentNamesKeepTheirFolder(t *testing.T) {
	if got := convoFolder("abc:123"); got != "abc123" {
		t.Errorf("convoFolder = %q, want abc123", got)
	}
	if got := agentPart("gpt-4o (copilot)"); got != "gpt-4o-copilot" {
		t.Errorf("agentPart = %q, want gpt-4o-copilot", got)
	}
}
