package mcpserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/session"
	"github.com/Coder8124/logos/internal/transcript"
)

// The whole loop an agent runs from resume's offer: harvest the record, read
// the transcript, send back what it concluded. The receipt counts what was kept
// and says what was dropped, because a reading that loses claims quietly reads
// as one that kept them.
func TestAnAgentCanDistilAnAutoRecordFromTheSlugResumeGaveIt(t *testing.T) {
	root := filepath.Join(t.TempDir(), "projects")
	dir := filepath.Join(root, "-work-shop")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(transcript.LogosClaudeProjectsEnv, root)
	lines := []string{
		`{"type":"user","sessionId":"lost-1","cwd":"/work/shop","timestamp":"2026-09-29T10:00:00Z","message":{"role":"user","content":"fix the checkout crash"}}`,
		`{"type":"assistant","sessionId":"lost-1","cwd":"/work/shop","timestamp":"2026-09-29T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go test ./cart"}}]}}`,
		`{"type":"user","sessionId":"lost-1","cwd":"/work/shop","timestamp":"2026-09-29T10:02:00Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":false,"content":"ok"}]}}`,
	}
	if err := os.WriteFile(filepath.Join(dir, "lost-1.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := &Session{Server: &Server{DB: testDB(t), vault: t.TempDir()}}
	rec, err := session.WriteAuto(s.vault, session.Checkpoint{Project: "shop", Agent: "claude-code",
		State: session.ActivityLogStateFor("lost-1"), Commands: []string{"go test ./cart"}})
	if err != nil {
		t.Fatal(err)
	}

	served, err := s.ingestHarvest(rec.Slug, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(served, "go test ./cart") || !strings.Contains(served, "served the transcript behind "+rec.Slug) {
		t.Fatalf("harvest did not serve the record's transcript:\n%s", served)
	}

	receipt, err := s.ingestDistil(rec.Slug, "claude-code", "", []string{"go test ./cart passed (turn 2)"},
		[]string{"guessed at the cause"}, nil, []string{"fixed it in checkout, because the prompt names the crash there (turn 1)"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(receipt, "1 verified, 0 didn't work, 1 decided") || !strings.Contains(receipt, "no turn cited") {
		t.Errorf("the receipt does not count what was kept and dropped:\n%s", receipt)
	}

	raw, _ := os.ReadFile(filepath.Join(s.vault, filepath.FromSlash(rec.Slug)+".md"))
	got := session.ParseCheckpoint(string(raw))
	if got.Inferred == nil || len(got.Inferred.Verified) != 1 || len(got.Inferred.Decided) != 1 {
		t.Errorf("the record holds %+v after distilling", got.Inferred)
	}
}
