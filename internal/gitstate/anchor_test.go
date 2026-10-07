package gitstate

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func commitAt(t *testing.T, dir, name, body, message string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(name)), 0o755); err != nil {
		t.Fatal(err)
	}
	commit(t, dir, name, body, message)
}

func TestARulingAboutAFileRewrittenSinceIsReportedAsDrifted(t *testing.T) {
	dir := repo(t)
	commitAt(t, dir, "internal/parse/reader.go", "load all\n", "buffered reader")
	_, anchor := Head(dir)
	commitAt(t, dir, "internal/parse/reader.go", "chunks\n", "read in chunks")
	commitAt(t, dir, "internal/parse/reader.go", "chunks, tuned\n", "tune the chunk size")

	d, ok := NewAnchors(dir).Since(anchor, "streaming fails — internal/parse/reader.go loads the whole file")
	if !ok {
		t.Fatal("two commits to the named file since the ruling were not reported")
	}
	if d.Commits != 2 || !reflect.DeepEqual(d.Paths, []string{"internal/parse/reader.go"}) || d.Commit != anchor {
		t.Fatalf("drift = %+v, want 2 commits to internal/parse/reader.go since %s", d, anchor)
	}
	if note := d.Note(); !strings.Contains(note, "changed in 2 commits since") || !strings.Contains(note, "may no longer hold") {
		t.Fatalf("note = %q", note)
	}
}

func TestABareFileNameIsFoundWhereverItLives(t *testing.T) {
	dir := repo(t)
	commitAt(t, dir, "internal/parse/reader.go", "load all\n", "buffered reader")
	_, anchor := Head(dir)
	commitAt(t, dir, "internal/parse/reader.go", "chunks\n", "read in chunks")

	d, ok := NewAnchors(dir).Since(anchor, "reader.go loads the whole file, so streaming fails")
	if !ok || d.Commits != 1 || d.Paths[0] != "reader.go" {
		t.Fatalf("drift = %+v, %v; want one commit to reader.go", d, ok)
	}
}

func TestARulingAboutAnUntouchedFileStillHolds(t *testing.T) {
	dir := repo(t)
	commitAt(t, dir, "internal/parse/reader.go", "load all\n", "buffered reader")
	_, anchor := Head(dir)
	// The repository moved, just not the file the ruling is about. Flagging
	// this would put the warning on every ruling in an active repository.
	commitAt(t, dir, "internal/other.go", "x\n", "unrelated")

	if d, ok := NewAnchors(dir).Since(anchor, "internal/parse/reader.go loads the whole file"); ok {
		t.Fatalf("an unchanged file was reported as drifted: %+v", d)
	}
}

func TestARulingThatNamesNoFileIsNeverFlagged(t *testing.T) {
	dir := repo(t)
	commitAt(t, dir, "a.go", "1\n", "one")
	_, anchor := Head(dir)
	commitAt(t, dir, "a.go", "2\n", "two")

	if d, ok := NewAnchors(dir).Since(anchor, "tried raising the timeout to 3.5s in v1.2; no good"); ok {
		t.Fatalf("prose with no file name was reported as drifted: %+v", d)
	}
}

func TestACommitFromAnotherRepositoryIsNotMeasured(t *testing.T) {
	dir := repo(t)
	commitAt(t, dir, "a.go", "1\n", "one")
	commitAt(t, dir, "a.go", "2\n", "two")

	// A sha this repository never had: another clone's, or one rebased away.
	// There is no history here to say the file moved since it.
	if d, ok := NewAnchors(dir).Since("deadbeefcafe", "a.go is wrong"); ok {
		t.Fatalf("a foreign commit was measured against: %+v", d)
	}
}

func TestARecordedShaThatIsNotHexNeverReachesGit(t *testing.T) {
	dir := repo(t)
	commitAt(t, dir, "a.go", "1\n", "one")
	commitAt(t, dir, "a.go", "2\n", "two")

	// Vault text. "HEAD~1" would be a working revision, "--all" an option.
	for _, sha := range []string{"HEAD~1", "--all", "main", ""} {
		if d, ok := NewAnchors(dir).Since(sha, "a.go is wrong"); ok {
			t.Fatalf("Since(%q) measured from a non-sha: %+v", sha, d)
		}
	}
}

func TestOutsideARepositoryNothingDrifts(t *testing.T) {
	if d, ok := NewAnchors(t.TempDir()).Since("abc1234", "a.go"); ok {
		t.Fatalf("drift outside a repository: %+v", d)
	}
}

func TestNamedFilesPicksPathsAndFileNamesOutOfProse(t *testing.T) {
	got := NamedFiles("`internal/parse/reader.go:42` loads it all (see reader.go, go.mod and https://x.io/a.go); v1.2 at 3.5GB, ../etc/passwd, /abs/b.go, internal/parse")
	want := []string{"internal/parse/reader.go", "go.mod"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NamedFiles = %q, want %q", got, want)
	}
}
