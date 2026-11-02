package session

import (
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
