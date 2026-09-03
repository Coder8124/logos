package scope

import (
	"os"
	"path/filepath"
	"testing"
)

// The bug this package was written for: a repository checked out under one
// name and called another. Without the marker the answer is the folder, which
// is how activity ended up filed under "brain" while every checkpoint sat
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
	write(t, filepath.Join(dir, MarkerFile), "\n# checked out as brain/, called logos\n\n  logos  \n# trailing\n")
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
