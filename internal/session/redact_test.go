package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A made-up token in GitHub's shape. Nothing checks it against GitHub; the
// only question is whether it reaches the vault.
const pastedToken = "ghp_R2d2C3poBb8Ee9Ff0Gg1Hh2Ii3Jj4Kk5Ll6M"

func vaultContains(t *testing.T, dir, needle string) []string {
	t.Helper()
	var hits []string
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		b, _ := os.ReadFile(path)
		if strings.Contains(string(b), needle) {
			hits = append(hits, path)
		}
		return nil
	})
	return hits
}

func TestACheckpointDoesNotWriteAPastedGitHubToken(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()

	c := &Checkpoint{
		Project:  "kestrel-one",
		Agent:    "claude",
		Task:     "fix the release upload",
		Commands: []string{`curl -H "Authorization: Bearer ` + pastedToken + `" https://api.github.com/user`},
		Verified: []string{"the upload works with GITHUB_TOKEN=" + pastedToken},
		Next:     "rotate " + pastedToken,
	}
	if err := Commit(db, dir, c); err != nil {
		t.Fatal(err)
	}
	if hits := vaultContains(t, dir, pastedToken); len(hits) > 0 {
		t.Fatalf("the token reached the vault in %v", hits)
	}
	if len(c.Redactions) == 0 {
		t.Fatal("the token was masked but the checkpoint does not say so, so no caller can announce it")
	}
}

func TestAWorkingNoteDoesNotWriteAPastedGitHubToken(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()
	SetVault(db, dir)
	t.Cleanup(func() { SetVault(db, "") })

	n, err := AddNote(db, "kestrel-one", "claude", "exported GITHUB_TOKEN="+pastedToken+" and the push worked")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(n.Text, pastedToken) {
		t.Fatalf("the token is in the note: %q", n.Text)
	}
	if len(n.Redactions) == 0 {
		t.Fatal("the note was masked without saying so")
	}
	notes, _ := Uncommitted(db, "kestrel-one")
	for _, got := range notes {
		if strings.Contains(got.Text, pastedToken) {
			t.Fatalf("the token is in the index: %q", got.Text)
		}
	}
	if hits := vaultContains(t, dir, pastedToken); len(hits) > 0 {
		t.Fatalf("the token reached the vault in %v", hits)
	}
}

// An agent's checkpoint is full of long mixed-case words that are not
// secrets. The transcript heuristic that masks high-entropy tokens would eat
// these, and a checkpoint whose verified line reads "[REDACTED] passes" has
// lost the one fact that made it verified.
func TestACheckpointKeepsTestNamesAndModulePaths(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()

	keep := []string{
		"TestWorkingNotesSurviveDeletingTheIndex",
		"github.com/Coder8124/logos/internal/session",
		"TestACheckpointDoesNotWriteAPastedGitHubToken",
	}
	c := &Checkpoint{
		Project:  "kestrel-one",
		Agent:    "claude",
		Verified: []string{"go test ./internal/session -run " + keep[0] + " passes in " + keep[1]},
		Next:     "run " + keep[2],
	}
	if err := Commit(db, dir, c); err != nil {
		t.Fatal(err)
	}
	for _, k := range keep {
		if hits := vaultContains(t, dir, k); len(hits) == 0 {
			t.Errorf("%s was masked out of the checkpoint", k)
		}
	}
	if len(c.Redactions) != 0 {
		t.Errorf("reported secrets in a checkpoint that has none: %+v", c.Redactions)
	}
}
