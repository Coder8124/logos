package session

import (
	"strings"
	"unicode/utf8"
)

import "testing"

// A project name long enough to pass the filesystem's limit reached the vault
// as a raw `mkdir …: file name too long` handed straight to the model — an
// error about the filesystem, for a name the product accepted without comment.
func TestAnOverlongProjectNameIsCutToSomethingTheFilesystemAccepts(t *testing.T) {
	got := safeScope(strings.Repeat("x", 300))
	if got == "" {
		t.Fatal("an overlong name was reduced to nothing, which reads as a name with no letters in it")
	}
	if len(got) > 255 {
		t.Errorf("safeScope produced a %d-byte segment, which the filesystem refuses", len(got))
	}
}

// Every name short enough to be a name must survive untouched, or the cap
// costs every project it was added to protect.
func TestANormalProjectNameIsNotCut(t *testing.T) {
	for _, name := range []string{"brain", "kestrel-one", "some-quite-long-but-reasonable-project-name"} {
		if got := safeScope(name); got != name {
			t.Errorf("%q became %q", name, got)
		}
	}
}

// The two levels of a scope are capped independently, so a long worktree name
// cannot eat the project name in front of it.
func TestEachLevelOfAScopeIsCappedOnItsOwn(t *testing.T) {
	got := safeScope("kestrel/" + strings.Repeat("y", 300))
	if !strings.HasPrefix(got, "kestrel/") {
		t.Errorf("the project name was lost to a long worktree name: %q", got)
	}
}

// #176: the cap was 120 bytes and applied on lookup too, so a project named in
// 121–255 bytes — a name every filesystem accepts and every earlier version
// wrote — was looked up under a key its history is not filed under.
func TestANameTheFilesystemAlwaysAcceptedIsNotCut(t *testing.T) {
	name := strings.Repeat("k", 150)
	if got := safeScope(name); got != name {
		t.Errorf("a %d-byte name was cut to %d bytes, and its earlier checkpoints are filed under the full name", len(name), len(got))
	}
}

// #175: the cut was by bytes, and a name in any non-Latin script could be cut
// inside a character — invalid UTF-8, which APFS refuses as a directory name.
func TestCuttingANonLatinNameLeavesWholeCharacters(t *testing.T) {
	// One ASCII byte first, so no byte cap lands on a character boundary by luck.
	got := safeScope("a" + strings.Repeat("日本", 60))
	if !utf8.ValidString(got) {
		t.Errorf("the cut split a character: %q", got[len(got)-4:])
	}
	if len(got) > 255 || got == "" {
		t.Errorf("cut to %d bytes", len(got))
	}
}
