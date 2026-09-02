package bootstrap

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Coder8124/brain/internal/memory"
)

// These tests build real repositories rather than faking git's output. The
// package exists to read what git actually says, so a fake would be testing the
// fixture and not the parsing — and every bug this code can have lives in the
// gap between the two.

func repo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	run(t, dir, "init", "--initial-branch=main")
	run(t, dir, "config", "user.email", "ada@example.com")
	run(t, dir, "config", "user.name", "Ada Lovelace")
	run(t, dir, "config", "commit.gpgsign", "false")
	return dir
}

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

// commitTo writes a file at path and commits it with the given subject and
// author, creating parent directories so hot spots can be exercised.
func commitTo(t *testing.T, dir, path, subject, author string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	// Content has to change or git makes no commit at all.
	body := fmt.Sprintf("%s\n%s\n", subject, path)
	if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "add", "-A")
	run(t, dir, "commit", "-m", subject, "--author", author)
}

func texts(cs []Candidate) string {
	var b strings.Builder
	for _, c := range cs {
		b.WriteString(c.Text)
		b.WriteString("\n")
	}
	return b.String()
}

func find(cs []Candidate, substr string) (Candidate, bool) {
	for _, c := range cs {
		if strings.Contains(c.Text, substr) {
			return c, true
		}
	}
	return Candidate{}, false
}

func TestNotARepositorySaysNothing(t *testing.T) {
	if got := FromGitHistory(t.TempDir(), 12); got != nil {
		t.Fatalf("a directory that is not a repository produced %d candidates", len(got))
	}
}

// The floor is the whole defence against confident nonsense on a young
// repository: with five commits the "main author" is whoever started it and the
// "hot spot" is the only file that exists.
func TestYoungRepositorySaysNothing(t *testing.T) {
	dir := repo(t)
	for i := 0; i < minCommits-1; i++ {
		commitTo(t, dir, fmt.Sprintf("src/f%d.go", i), fmt.Sprintf("add f%d", i), "Ada Lovelace <ada@example.com>")
	}
	if got := FromGitHistory(dir, 12); got != nil {
		t.Fatalf("a repository below the floor produced %d candidates:\n%s", len(got), texts(got))
	}
}

func TestDominantAuthorIsNamed(t *testing.T) {
	dir := repo(t)
	for i := 0; i < 24; i++ {
		commitTo(t, dir, fmt.Sprintf("core/f%d.go", i), fmt.Sprintf("add f%d", i), "Ada Lovelace <ada@example.com>")
	}
	for i := 0; i < 3; i++ {
		commitTo(t, dir, fmt.Sprintf("docs/d%d.md", i), fmt.Sprintf("doc %d", i), "Grace Hopper <grace@example.com>")
	}
	got := FromGitHistory(dir, 12)
	c, ok := find(got, "Ada Lovelace")
	if !ok {
		t.Fatalf("dominant author not named:\n%s", texts(got))
	}
	if c.Kind != memory.Person {
		t.Errorf("author memory has kind %q, want %q", c.Kind, memory.Person)
	}
	if c.Confidence >= 0.9 {
		t.Errorf("confidence %.2f is at hand-stated level; a derived fact must rank below one a human asserted", c.Confidence)
	}
	if c.Evidence == "" {
		t.Error("no evidence recorded, so the user cannot see why this was proposed")
	}
}

// A repository with no majority author must not invent one. This is the case
// that would send an arriving agent to the wrong person, so it is asserted
// rather than left to the switch.
func TestSplitAuthorshipIsNotGivenAnOwner(t *testing.T) {
	dir := repo(t)
	authors := []string{
		"Ada Lovelace <ada@example.com>",
		"Grace Hopper <grace@example.com>",
		"Alan Turing <alan@example.com>",
	}
	for i := 0; i < 30; i++ {
		commitTo(t, dir, fmt.Sprintf("core/f%d.go", i), fmt.Sprintf("add f%d", i), authors[i%3])
	}
	got := FromGitHistory(dir, 12)
	if _, ok := find(got, "writes most of this codebase"); ok {
		t.Fatalf("an evenly split repository was given a main author:\n%s", texts(got))
	}
	if _, ok := find(got, "no single main author"); !ok {
		t.Fatalf("split authorship was not reported at all:\n%s", texts(got))
	}
}

func TestHotspotsNameTheBusyAreas(t *testing.T) {
	dir := repo(t)
	for i := 0; i < 20; i++ {
		commitTo(t, dir, fmt.Sprintf("internal/memory/f%d.go", i), fmt.Sprintf("add f%d", i), "Ada Lovelace <ada@example.com>")
	}
	for i := 0; i < 4; i++ {
		commitTo(t, dir, fmt.Sprintf("docs/d%d.md", i), fmt.Sprintf("doc %d", i), "Ada Lovelace <ada@example.com>")
	}
	got := FromGitHistory(dir, 12)
	c, ok := find(got, "Change concentrates in")
	if !ok {
		t.Fatalf("no hot spot reported:\n%s", texts(got))
	}
	if !strings.Contains(c.Text, "internal/memory/") {
		t.Errorf("busiest area missing from %q", c.Text)
	}
	if c.Kind != memory.Context {
		t.Errorf("hot spot has kind %q, want %q", c.Kind, memory.Context)
	}
}

// Two runs over an unchanged repository must produce identical text. A memory
// whose wording drifts on every bootstrap is one the supersession logic will
// keep re-filing as a new fact.
func TestOutputIsStableAcrossRuns(t *testing.T) {
	dir := repo(t)
	for i := 0; i < 24; i++ {
		// Equal counts in two areas, so ties are actually exercised.
		area := "alpha"
		if i%2 == 0 {
			area = "beta"
		}
		commitTo(t, dir, fmt.Sprintf("%s/pkg/f%d.go", area, i), fmt.Sprintf("add f%d", i), "Ada Lovelace <ada@example.com>")
	}
	first := texts(FromGitHistory(dir, 12))
	for i := 0; i < 3; i++ {
		if again := texts(FromGitHistory(dir, 12)); again != first {
			t.Fatalf("output is not stable:\nfirst:\n%s\nlater:\n%s", first, again)
		}
	}
}

func TestConventionalCommitsDetected(t *testing.T) {
	dir := repo(t)
	for i := 0; i < 24; i++ {
		commitTo(t, dir, fmt.Sprintf("src/f%d.go", i), fmt.Sprintf("feat(core): add f%d", i), "Ada Lovelace <ada@example.com>")
	}
	got := FromGitHistory(dir, 12)
	if _, ok := find(got, "Conventional Commits"); !ok {
		t.Fatalf("conventional commits not detected:\n%s", texts(got))
	}
}

// A repository with no dominant style must be told nothing about style, rather
// than being told the plurality one — an agent matching a 50% convention gets
// it wrong half the time, which is worse than having no opinion.
func TestMixedCommitStyleIsNotReported(t *testing.T) {
	dir := repo(t)
	for i := 0; i < 24; i++ {
		subject := fmt.Sprintf("feat: add f%d", i)
		if i%2 == 0 {
			subject = fmt.Sprintf("random lowercase thing %d.", i)
		}
		commitTo(t, dir, fmt.Sprintf("src/f%d.go", i), subject, "Ada Lovelace <ada@example.com>")
	}
	got := FromGitHistory(dir, 12)
	if _, ok := find(got, "Conventional Commits"); ok {
		t.Errorf("a 50/50 repository was reported as conventional:\n%s", texts(got))
	}
	if _, ok := find(got, "capitalised sentences"); ok {
		t.Errorf("a 50/50 repository was reported as capitalised:\n%s", texts(got))
	}
}

func TestEveryCandidateCarriesEvidenceAndSubHumanConfidence(t *testing.T) {
	dir := repo(t)
	for i := 0; i < 30; i++ {
		commitTo(t, dir, fmt.Sprintf("internal/x/f%d.go", i), fmt.Sprintf("Add f%d", i), "Ada Lovelace <ada@example.com>")
	}
	got := FromGitHistory(dir, 12)
	if len(got) == 0 {
		t.Fatal("nothing produced for a repository well past the floor")
	}
	for _, c := range got {
		if strings.TrimSpace(c.Text) == "" {
			t.Error("a candidate has no text")
		}
		if strings.TrimSpace(c.Evidence) == "" {
			t.Errorf("candidate %q carries no evidence", c.Text)
		}
		if c.Confidence <= 0 || c.Confidence >= 0.9 {
			t.Errorf("candidate %q has confidence %.2f, outside the derived-fact band", c.Text, c.Confidence)
		}
		if c.Kind == "" {
			t.Errorf("candidate %q has no kind", c.Text)
		}
	}
}

func TestIsConventional(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"feat: add a thing", true},
		{"fix(parser): handle empty input", true},
		{"feat!: breaking", true},
		{"feat(api)!: breaking", true},
		{"Add a thing", false},
		{"WIP", false},
		{"", false},
		{"Merge branch 'main' into side", false},
		{"http://example.com is down", false},
		{"feat:", false},
	} {
		if got := isConventional(tc.in); got != tc.want {
			t.Errorf("isConventional(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestTopDirs(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"README.md", "(root)"},
		{"internal/memory/store.go", "internal/memory/"},
		{"cmd/brain/main.go", "cmd/brain/"},
		{"docs/x.md", "docs/"},
	} {
		if got := topDirs(tc.in, 2); got != tc.want {
			t.Errorf("topDirs(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
