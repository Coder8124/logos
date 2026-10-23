package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/memory"
)

// standInAutoMemory writes notes where Claude Code keeps auto memory for the
// working directory. The folder name is spelled out by hand, not taken from
// autoMemoryDir, so a wrong encoding fails here instead of agreeing with itself.
func standInAutoMemory(t *testing.T, notes map[string]string) string {
	t.Helper()
	standInRules(t, nil)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	name := strings.NewReplacer("/", "-", ".", "-", "_", "-").Replace(cwd)
	dir := filepath.Join(os.Getenv("HOME"), ".claude", "projects", name, "memory")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for file, body := range notes {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const feedbackNote = "---\nname: no-trailers\ndescription: no Co-Authored-By on commits\nmetadata:\n  type: feedback\n---\n\n" +
	"Commits carry no Co-Authored-By trailer.\n\n**Why:** the user asked.\n**How to apply:** every commit.\n"

const projectNote = "---\nname: release-freeze\ndescription: freeze before the launch\nmetadata:\n  type: project\n---\n\n" +
	"Merges freeze on 2026-11-05 for the launch.\n"

// What Claude Code's auto memory learned is only Claude Code's: Codex and a
// teammate's agent never read ~/.claude. Importing it queues each note for
// review, with its kind, and leaves the index file out.
func TestClaudeAutoMemoryNotesAreQueuedOncePerNoteWithTheirKind(t *testing.T) {
	standInAutoMemory(t, map[string]string{
		"MEMORY.md":           "- [No trailers](feedback_no_trailers.md) — no Co-Authored-By\n",
		"feedback_trailer.md": feedbackNote,
		"project_freeze.md":   projectNote,
		"scratch.txt":         "not a note",
	})

	out := importRules(t, claudeAutoMemorySource)
	if !strings.Contains(out, "Imported 2, skipped 0 (already present), refused 0") {
		t.Errorf("the report does not count two notes:\n%s", out)
	}
	if strings.Contains(out, "rule") {
		t.Errorf("auto memory notes were called rules:\n%s", out)
	}
	got := pendingFrom(t, claudeAutoMemorySource)
	want := []string{
		"Commits carry no Co-Authored-By trailer. **Why:** the user asked. **How to apply:** every commit.",
		"Merges freeze on 2026-11-05 for the launch.",
	}
	if !slices.Equal(texts(got), want) {
		t.Fatalf("queued %q, want %q", texts(got), want)
	}
	kinds := map[string]memory.Kind{}
	for _, m := range got {
		kinds[m.Text] = m.Kind
		if m.Project != "shop" {
			t.Errorf("a note for this repository was queued for project %q", m.Project)
		}
	}
	if kinds[want[0]] != memory.Preference || kinds[want[1]] != memory.Context {
		t.Errorf("feedback and project notes became %v and %v", kinds[want[0]], kinds[want[1]])
	}
	if all := importedMemoriesFrom(t, claudeAutoMemorySource); len(all) != 0 {
		t.Errorf("an agent-written note is recallable before anyone accepted it: %+v", all)
	}
}

// A note whose body is empty still said something in its description; one
// with neither is nothing, and is not queued as an empty proposal.
func TestAnAutoMemoryNoteWithNoBodyIsQueuedAsItsDescription(t *testing.T) {
	standInAutoMemory(t, map[string]string{
		"reference_dash.md": "---\nname: dash\ndescription: \"latency dashboard is grafana/d/api\"\nmetadata:\n  type: reference\n---\n",
		"empty.md":          "---\nname: empty\n---\n\n",
	})
	importRules(t, claudeAutoMemorySource)
	got := pendingFrom(t, claudeAutoMemorySource)
	if len(got) != 1 || got[0].Text != "latency dashboard is grafana/d/api" || got[0].Kind != memory.Fact {
		t.Errorf("queued %+v, want the description alone as a fact", got)
	}
}

// Claude Code may keep its folder elsewhere. An empty import names the folder
// it looked in, so a moved one reads as moved, not as nothing ever saved.
func TestNothingToImportFromAutoMemoryNamesTheFolderItLookedIn(t *testing.T) {
	standInRules(t, nil)
	config := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	out := importRules(t, claudeAutoMemorySource)
	if !strings.Contains(out, "Nothing to import") || !strings.Contains(out, filepath.Join(config, "projects")) {
		t.Errorf("the empty import does not name where it looked:\n%s", out)
	}
}

func TestAutoMemoryIsReadFromClaudeConfigDirWhenItIsSet(t *testing.T) {
	standInRules(t, nil)
	config := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	cwd, _ := os.Getwd()
	dir := filepath.Join(config, "projects", strings.NewReplacer("/", "-", ".", "-", "_", "-").Replace(cwd), "memory")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "project_freeze.md"), []byte(projectNote), 0o644); err != nil {
		t.Fatal(err)
	}
	importRules(t, claudeAutoMemorySource)
	if n := len(pendingFrom(t, claudeAutoMemorySource)); n != 1 {
		t.Errorf("queued %d notes from CLAUDE_CONFIG_DIR, want 1", n)
	}
}

func TestImportingAutoMemoryTwiceQueuesNothingTheSecondTime(t *testing.T) {
	standInAutoMemory(t, map[string]string{"project_freeze.md": projectNote})
	importRules(t, claudeAutoMemorySource)
	if again := importRules(t, claudeAutoMemorySource); !strings.Contains(again, "Imported 0, skipped 1 (already present)") {
		t.Errorf("a second import did not find the note already queued:\n%s", again)
	}
}

// encodedAutoMemoryDir is the folder Claude Code would use for path, spelled
// out by hand so a wrong encoding in autoMemoryDir cannot agree with itself.
func encodedAutoMemoryDir(t *testing.T, projects, path string) string {
	t.Helper()
	dir := filepath.Join(projects, strings.NewReplacer("/", "-", ".", "-", "_", "-").Replace(path), "memory")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// Claude Code keeps one auto memory per repository, under the main checkout's
// path. Read from a worktree, the import looked under the worktree's path and
// said there was nothing.
func TestAutoMemoryIsFoundFromAWorktreeOfTheRepository(t *testing.T) {
	standInRules(t, nil)
	projects := t.TempDir()
	t.Setenv("LOGOS_CLAUDE_PROJECTS", projects)
	repo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	git(t, repo, "init", "-q")
	git(t, repo, "commit", "-q", "--allow-empty", "-m", "first")
	tree := filepath.Join(filepath.Dir(repo), filepath.Base(repo)+"-tree")
	git(t, repo, "worktree", "add", "-q", tree)
	dir := encodedAutoMemoryDir(t, projects, repo)
	if err := os.WriteFile(filepath.Join(dir, "project_freeze.md"), []byte(projectNote), 0o644); err != nil {
		t.Fatal(err)
	}
	out := importRules(t, claudeAutoMemorySource, "--dir", tree)
	if n := len(pendingFrom(t, claudeAutoMemorySource)); n != 1 {
		t.Errorf("queued %d notes from a worktree, want the repository's 1:\n%s", n, out)
	}
}

// Claude Code records the physical path. Reached through a symlink, the import
// encoded the link's path and missed the folder.
func TestAutoMemoryIsFoundThroughASymlinkedPath(t *testing.T) {
	standInRules(t, nil)
	projects := t.TempDir()
	t.Setenv("LOGOS_CLAUDE_PROJECTS", projects)
	real, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "code")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	dir := encodedAutoMemoryDir(t, projects, real)
	if err := os.WriteFile(filepath.Join(dir, "project_freeze.md"), []byte(projectNote), 0o644); err != nil {
		t.Fatal(err)
	}
	importRules(t, claudeAutoMemorySource, "--dir", link)
	if n := len(pendingFrom(t, claudeAutoMemorySource)); n != 1 {
		t.Errorf("queued %d notes through a symlink, want 1", n)
	}
}

// A feature announces what it skipped. A folder of three notes with one empty
// and one not a note reported one import and nothing else.
func TestAnAutoMemoryImportNamesEachFileItLeftOut(t *testing.T) {
	standInAutoMemory(t, map[string]string{
		"project_freeze.md": projectNote,
		"empty.md":          "---\nname: empty\n---\n",
		"scratch.txt":       "not a note",
	})
	out := importRules(t, claudeAutoMemorySource)
	for _, want := range []string{"empty.md: empty", "scratch.txt: not a note"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not say it left out %q:\n%s", want, out)
		}
	}
}

// The frontmatter is YAML. A folded description and a type nested elsewhere
// were read line by line as the memory ">-" and as an unknown type.
func TestAutoMemoryFrontmatterIsReadAsYAML(t *testing.T) {
	standInAutoMemory(t, map[string]string{
		"a.md": "---\nname: a\ndescription: >-\n  the staging database\n  resets nightly\nsource:\n  type: url\nmetadata:\n  type: project\n---\n",
		"b.md": "---\n---\nA bare body under an empty block.\n",
	})
	importRules(t, claudeAutoMemorySource)
	got := map[string]memory.Kind{}
	for _, m := range pendingFrom(t, claudeAutoMemorySource) {
		got[m.Text] = m.Kind
	}
	if k, ok := got["the staging database resets nightly"]; !ok || k != memory.Context {
		t.Errorf("the folded description or its nested type was misread: %v", got)
	}
	if _, ok := got["A bare body under an empty block."]; !ok {
		t.Errorf("an empty frontmatter block leaked into the text: %v", got)
	}
}

// A user note is who the person is, and that holds in every repository.
func TestAUserAutoMemoryNoteIsForEveryProject(t *testing.T) {
	standInAutoMemory(t, map[string]string{
		"user_role.md": "---\nname: role\nmetadata:\n  type: user\n---\nA data scientist, new to Go.\n",
	})
	importRules(t, claudeAutoMemorySource)
	got := pendingFrom(t, claudeAutoMemorySource)
	if len(got) != 1 || got[0].Project != "" {
		t.Errorf("a user note was scoped to a project: %+v", got)
	}
}

// Run from a subdirectory, the empty report named a folder for the
// subdirectory while the read had looked under the repository's root.
func TestAnEmptyAutoMemoryImportFromASubdirectoryNamesTheFolderItRead(t *testing.T) {
	standInRules(t, nil)
	config := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	if err := os.MkdirAll("sub", 0o755); err != nil {
		t.Fatal(err)
	}
	out := importRules(t, claudeAutoMemorySource, "--dir", "sub")
	if !strings.Contains(out, "Nothing to import") || strings.Contains(out, "-sub") {
		t.Errorf("the empty report names the subdirectory's folder, not the one read:\n%s", out)
	}
}
