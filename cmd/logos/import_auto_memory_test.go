package main

import (
	"os"
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
