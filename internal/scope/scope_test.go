package scope

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The bug this package was written for: a repository checked out under one
// name and called another. Without the marker the answer is the folder, which
// is how activity ended up filed under "logos" while every checkpoint sat
// under "logos".
func TestMarkerRenamesTheDirectory(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, MarkerFile), "logos\n")
	if got := Name(dir); got != "logos" {
		t.Errorf("Name = %q, want %q", got, "logos")
	}
	if got := Basename(dir); got == "logos" {
		t.Error("Basename must not read the marker; that is Name's job")
	}
}

// A hook runs wherever the agent happens to be standing, which is often not
// the repository root. The marker has to be found from below or it is only
// half a fix.
func TestMarkerIsFoundFromASubdirectory(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, MarkerFile), "logos\n")
	deep := filepath.Join(root, "internal", "mcpserver")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := Name(deep); got != "logos" {
		t.Errorf("Name from a subdirectory = %q, want %q", got, "logos")
	}
}

// The marker is a committed file a person will want to explain, so a comment
// and a blank line must not become the project name.
func TestMarkerSkipsCommentsAndBlankLines(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, MarkerFile), "\n# checked out as logos/, called logos\n\n  logos  \n# trailing\n")
	if got := Name(dir); got != "logos" {
		t.Errorf("Name = %q, want %q", got, "logos")
	}
}

// A marker holding only comments is not a name. Falling back to the basename
// is right; returning "" would silently make the work global.
func TestMarkerWithNoNameFallsBackToTheBasename(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, MarkerFile), "# nothing but a comment\n")
	if got, want := Name(dir), filepath.Base(dir); got != want {
		t.Errorf("Name = %q, want the basename %q", got, want)
	}
}

// A name becomes a directory under sessions/, and a "/" in it is how a
// worktree sub-scope is spelled. A marker must not be able to forge one.
func TestMarkerCannotForgeAWorktreeSubScope(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, MarkerFile), "logos/feature-x\n")
	if got := Name(dir); got != "logos-feature-x" {
		t.Errorf("Name = %q, want the separator replaced", got)
	}
}

// The nearest marker wins, so a repository inside a directory that names
// itself is not renamed by its parent.
func TestNearestMarkerWins(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, MarkerFile), "outer\n")
	inner := filepath.Join(root, "vendored")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(inner, MarkerFile), "inner\n")
	if got := Name(inner); got != "inner" {
		t.Errorf("Name = %q, want %q", got, "inner")
	}
}

// No marker anywhere is the common case, and it must behave exactly as the
// basename rule did before this package existed.
func TestNoMarkerIsTheBasename(t *testing.T) {
	dir := t.TempDir()
	if got, want := Name(dir), filepath.Base(dir); got != want {
		t.Errorf("Name = %q, want %q", got, want)
	}
}

// Global, not "unknown": a home directory is where a host is launched when the
// user has not opened a project, and naming one after it would pool every
// unrelated session into a single bucket.
func TestHomeAndRootAreGlobal(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory on this machine")
	}
	if got := Basename(home); got != "" {
		t.Errorf("Basename(home) = %q, want the empty (global) scope", got)
	}
	if got := Basename("/"); got != "" {
		t.Errorf("Basename(\"/\") = %q, want the empty (global) scope", got)
	}
	if got := Basename(""); got != "" {
		t.Errorf("Basename(\"\") = %q, want the empty (global) scope", got)
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A host launched in a subfolder filed its checkpoint under the subfolder's
// name, and the next session opened at the repository root never saw it. The
// repository is the project; a monorepo that wants one per service says so with
// a marker, which is still read first.
func TestASubfolderOfAGitRepositoryIsTheRepository(t *testing.T) {
	for _, git := range []string{"directory", "file"} {
		repo := filepath.Join(t.TempDir(), "api")
		deep := filepath.Join(repo, "internal", "db")
		if err := os.MkdirAll(deep, 0o755); err != nil {
			t.Fatal(err)
		}
		// A worktree or a submodule has a .git file rather than a directory.
		if git == "directory" {
			if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
				t.Fatal(err)
			}
		} else {
			write(t, filepath.Join(repo, ".git"), "gitdir: /elsewhere\n")
		}
		if got := Name(deep); got != "api" {
			t.Errorf("with a .git %s, Name(subfolder) = %q, want %q", git, got, "api")
		}
	}
}

// A home directory kept under git for its dotfiles is not a project, and every
// folder beneath it would otherwise be filed under the user's name.
func TestAGitRepositoryAtHomeDoesNotNameWhatIsUnderIt(t *testing.T) {
	home := filepath.Join(t.TempDir(), "someone")
	t.Setenv("HOME", home)
	deep := filepath.Join(home, "notes", "drafts")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(home, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := Name(deep); got != "drafts" {
		t.Errorf("Name under a git-tracked home = %q, want the folder's own name %q", got, "drafts")
	}
}

// A relative or ~ path was walked as written, and filepath.Dir("..") is ".", so
// the marker search for "../etc" climbed into the working directory: memory add
// --project ../etc, run from this repository, was filed under "brain".
func TestAPathArgumentIsNamedFromWhereItPointsNotFromWhereTheCallerStands(t *testing.T) {
	here := t.TempDir()
	if err := os.WriteFile(filepath.Join(here, ".logos-project"), []byte("brain\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(here)
	t.Setenv("HOME", t.TempDir())

	for arg, want := range map[string]string{
		"../etc":         "etc",
		"~/code/kestrel": "kestrel",
		"a/code/kestrel": "kestrel",
	} {
		if got := NormalizeArg(arg); got != want {
			t.Errorf("NormalizeArg(%q) = %q, want %q", arg, got, want)
		}
	}
}

// A deny typed in a subfolder has to cover the session opened at the top of
// the repository, and one written in a subfolder has to hold below it.
func TestDenyAtTheRootCoversEverySubdirectory(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "internal", "api")
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if Denied(root) {
		t.Fatal("a repository nobody denied reads as denied")
	}
	path, err := Deny(deep)
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(root, DenyMarker) {
		t.Errorf("deny from a subfolder wrote %s, not at the repository root", path)
	}
	if !Denied(root) || !Denied(deep) {
		t.Error("the marker at the root does not cover the root and its subfolders")
	}
}

// --undo that left a marker standing in a subfolder would report success on a
// repository that is still off below it.
func TestUndenyRemovesEveryMarkerOnTheWayUp(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "web")
	for _, d := range []string{".git", ".logos", filepath.Join("web", ".logos")} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(root, DenyMarker), "x\n")
	write(t, filepath.Join(sub, DenyMarker), "x\n")
	removed, err := Undeny(sub)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 2 || Denied(sub) || Denied(root) {
		t.Errorf("undeny removed %v and left the tree denied=%v", removed, Denied(sub))
	}
	if _, err := os.Stat(filepath.Join(root, ".logos")); err == nil {
		t.Error("undeny left the empty .logos/ it was the only reason for")
	}
}

// A folder that is not a repository is its own deny root, so its subfolders
// are covered only if the walk goes past it. A session opened in notes/drafts
// after `logos deny` in notes was tracked.
func TestDenyInAPlainDirectoryCoversItsSubfolders(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "drafts")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Deny(dir); err != nil {
		t.Fatal(err)
	}
	if !Denied(sub) {
		t.Error("a subfolder of a denied plain directory is tracked")
	}
	if removed, err := Undeny(sub); err != nil || len(removed) != 1 || Denied(sub) {
		t.Errorf("undo from the subfolder: removed %v, err %v, still denied %v", removed, err, Denied(sub))
	}
}

// otherCase is p spelled in capitals, for a disk that ignores case (macOS and
// Windows by default) to answer as the same directory. A disk that honours
// case has no such second spelling, so there is nothing to test.
func otherCase(t *testing.T, p string) string {
	t.Helper()
	up := strings.ToUpper(p)
	if up == p {
		t.Skip("the path has no letters to change the case of")
	}
	if _, err := os.Stat(up); err != nil {
		t.Skip("this disk honours case, so the path has only one spelling")
	}
	return up
}

// A shell standing in ~/ideaprojects/brain on macOS is in the same folder as
// one in ~/IdeaProjects/brain. Named as typed, the one project split into
// "brain" and "BRAIN", and neither session saw the other's checkpoints.
func TestAFolderReachedUnderAnotherCaseIsNamedAsTheDiskSpellsIt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Kestrel")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := Name(otherCase(t, dir)); got != "Kestrel" {
		t.Errorf("Name = %q, want %q", got, "Kestrel")
	}
}

// The walk for a repository stops at the home directory so a dotfiles repo
// there is never a project. Comparing the walk's spelling with $HOME's let a
// differently cased path walk straight past it and name the session after the
// user.
func TestTheRepositoryWalkStopsAtHomeHoweverThePathWasSpelled(t *testing.T) {
	home := filepath.Join(t.TempDir(), "Home")
	notes := filepath.Join(home, "notes")
	if err := os.MkdirAll(filepath.Join(home, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(notes, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	if got := Name(otherCase(t, notes)); got != "notes" {
		t.Errorf("Name = %q, want %q: the home directory's dotfiles repo was taken for the project", got, "notes")
	}
}

// A path the disk does not have yet — a project folder about to be created —
// has no other spelling to find, and must come back as given rather than
// emptied or truncated.
func TestOnDiskKeepsTheMissingPartOfAPathAsGiven(t *testing.T) {
	base := t.TempDir()
	want := filepath.Join(OnDisk(base), "not", "made", "Yet")
	if got := OnDisk(filepath.Join(base, "not", "made", "Yet")); got != want {
		t.Errorf("OnDisk = %q, want %q", got, want)
	}
}
