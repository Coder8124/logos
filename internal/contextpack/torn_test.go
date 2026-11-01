package contextpack

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/session"
)

// #367: a newer checkpoint that is empty or cut off partway through a save was
// skipped, and resume showed the one before it as where work left off, with no
// word that a newer file was there at all.
func TestResumeSaysANewerCheckpointCouldNotBeRead(t *testing.T) {
	ix := seedVault(t)
	now := time.Now()
	if err := session.Commit(ix.DB, ix.Vault, &session.Checkpoint{
		Project: "kestrel-one", Agent: "claude", Next: "re-quote the display stack",
		TS: now.Add(-time.Hour).Unix(),
	}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(ix.Vault, session.CheckpointDir, "kestrel-one", session.ConvosDir, session.UnattachedFolder)
	torn := now.Format("20060102-150405") + "-codex.md"
	if err := os.WriteFile(filepath.Join(dir, torn), []byte("---\ntype: checkpoint\nproject: kestrel-one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := Build(ix, nil, "", Request{Task: "continue", Hint: "kestrel-one", Now: now.Unix()})
	if err != nil {
		t.Fatal(err)
	}
	out := p.Render()
	if !strings.Contains(out, "re-quote the display stack") {
		t.Fatalf("the intact checkpoint was not handed over:\n%s", out)
	}
	if !strings.Contains(out, torn) {
		t.Errorf("resume does not name the newer checkpoint it could not read:\n%s", out)
	}
}
