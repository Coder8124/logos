package mcpserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/ingest"
	"github.com/Coder8124/logos/internal/session"
	"github.com/Coder8124/logos/internal/transcript"
)

// harvestAutoRecord harvests an auto record over MCP and returns the receipt
// line and the vault, with the ingest grant given and keeping set as asked.
func harvestAutoRecord(t *testing.T, keep bool) (receipt, vaultDir string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "projects")
	dir := filepath.Join(root, "-work-shop")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(transcript.LogosClaudeProjectsEnv, root)
	line := `{"type":"user","sessionId":"lost-1","cwd":"/work/shop","timestamp":"2026-09-29T10:00:00Z","message":{"role":"user","content":"fix the checkout crash"}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "lost-1.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Session{Server: &Server{DB: testDB(t), vault: t.TempDir()}}
	if err := os.MkdirAll(filepath.Join(s.vault, ".logos"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ingest.ConsentPath(s.vault), []byte(`{"granted":"2026-10-01T00:00:00Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if keep {
		if _, err := ingest.SetKeepTranscripts(s.vault, true, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	rec, err := session.WriteAuto(s.vault, session.Checkpoint{Project: "shop", Agent: "claude-code",
		State: session.ActivityLogStateFor("lost-1"), Turns: 1})
	if err != nil {
		t.Fatal(err)
	}
	served, err := s.ingestHarvest(rec.Slug, 0)
	if err != nil {
		t.Fatal(err)
	}
	first, _, _ := strings.Cut(served, "\n")
	return first, s.vault
}

func TestTheHarvestReceiptNamesTheArchivedTranscriptSizeWhenKeepingIsOn(t *testing.T) {
	receipt, v := harvestAutoRecord(t, true)
	if !strings.Contains(receipt, "raw transcript archived (") || !strings.Contains(receipt, " B)") {
		t.Errorf("the receipt does not name the raw copy and its size: %s", receipt)
	}
	if session.FindTranscript(v, "lost-1") == "" {
		t.Error("the receipt said archived but no transcript is in the vault")
	}
}

// Off is the default, and off is today's behaviour exactly: the same receipt,
// and nothing of the transcript in the vault.
func TestNothingIsArchivedWhenKeepingTranscriptsIsOff(t *testing.T) {
	receipt, v := harvestAutoRecord(t, false)
	if strings.Contains(receipt, "transcript archived") || strings.Contains(receipt, "not archived") {
		t.Errorf("with keeping off the receipt talks about archiving: %s", receipt)
	}
	if p := session.FindTranscript(v, "lost-1"); p != "" {
		t.Errorf("with keeping off a transcript landed in the vault at %s", p)
	}
}
