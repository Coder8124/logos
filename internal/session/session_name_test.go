package session

import "strings"

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
