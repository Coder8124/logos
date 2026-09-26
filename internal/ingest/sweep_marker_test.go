package ingest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/scope"
	"github.com/Coder8124/logos/internal/transcript"
)

// #196: a repository checked out as code/web whose marker names it storefront
// files its checkpoints under storefront, but its Claude Code transcripts sit
// in a folder slugged from code/web. The slug prefilter ruled every one of them
// out before reading the cwd that scope.Name would have named storefront, so a
// killed session there was never recorded, and nothing was said.
func TestAKilledSessionIsSweptWhenAMarkerNamesTheProjectUnlikeItsFolder(t *testing.T) {
	now := time.Now()
	repo := filepath.Join(t.TempDir(), "code", "web")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, scope.MarkerFile), []byte("storefront\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	root := filepath.Join(t.TempDir(), "projects")
	t.Setenv(transcript.LogosClaudeProjectsEnv, root)
	t.Setenv(transcript.LogosCursorStorageEnv, t.TempDir())
	t.Setenv(transcript.LogosCodexSessionsEnv, t.TempDir())
	slug := strings.NewReplacer("/", "-", ".", "-", "_", "-").Replace(repo)
	dir := filepath.Join(root, slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	stamp := func(t time.Time) string { return t.UTC().Format(time.RFC3339) }
	start, end := now.Add(-3*time.Hour), now.Add(-2*time.Hour)
	lines := []string{
		`{"type":"user","sessionId":"lost-web","cwd":"` + repo + `","timestamp":"` + stamp(start) + `","message":{"role":"user","content":"fix the checkout crash"}}`,
		`{"type":"assistant","sessionId":"lost-web","cwd":"` + repo + `","timestamp":"` + stamp(start) + `","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go test ./cart"}}]}}`,
		`{"type":"user","sessionId":"lost-web","cwd":"` + repo + `","timestamp":"` + stamp(end) + `","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":false,"content":"ok"}]}}`,
	}
	path := filepath.Join(dir, "lost-web.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, end, end); err != nil {
		t.Fatal(err)
	}

	wrote, _, problems := Sweep(t.TempDir(), "storefront", now)
	if len(problems) != 0 || len(wrote) != 1 {
		t.Fatalf("the killed session was not recorded under the marker's name: wrote %d, problems %v", len(wrote), problems)
	}
	if again, _, _ := Sweep(t.TempDir(), "web", now); len(again) != 0 {
		t.Errorf("the session was also recorded under its folder's name, which the marker overrides: %+v", again)
	}
}

// The marker check reads a transcript's first cwd before passing it over. One
// that records no cwd there could not be told apart, and was parsed in full on
// every resume of every project — what the slug prefilter exists to avoid. It
// is judged by its folder, as it was before the marker check. Unreadable here,
// so reading it at all shows up as a problem.
func TestATranscriptInAnotherProjectsFolderWithNoCwdIsNotRead(t *testing.T) {
	now := time.Now()
	root := filepath.Join(t.TempDir(), "projects")
	t.Setenv(transcript.LogosClaudeProjectsEnv, root)
	t.Setenv(transcript.LogosCursorStorageEnv, t.TempDir())
	t.Setenv(transcript.LogosCodexSessionsEnv, t.TempDir())
	dir := filepath.Join(root, "-Users-me-code-kestrel")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "other.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	end := now.Add(-2 * time.Hour)
	if err := os.Chtimes(path, end, end); err != nil {
		t.Fatal(err)
	}

	if _, _, problems := Sweep(t.TempDir(), "storefront", now); len(problems) != 0 {
		t.Errorf("a transcript of another project's folder was read on a storefront resume: %v", problems)
	}
}
