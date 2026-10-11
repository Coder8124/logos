package ops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/auto"
	"github.com/Coder8124/logos/internal/session"
)

// An agent in a linked worktree checkpoints one level down, under
// sessions/<project>/<worktree>/, and that checkpoint is the one the last
// session read the report beside.
func TestAWorktreeCheckpointEndsWhatResumeReportsOfAuto(t *testing.T) {
	vault := t.TempDir()
	if err := auto.Record(vault, auto.Entry{At: time.Now().Add(-time.Hour), Switch: auto.Checkpoint, Project: "shop", N: 1}); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-30 * time.Minute)
	c := session.Checkpoint{Project: "shop/fix-auth", Agent: "claude-code", Task: "fix auth", Next: "ship it", TS: at.Unix()}
	dir := filepath.Join(vault, session.CheckpointDir, "shop", "fix-auth")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, at.Format("20060102-150405")+"-claude-code.md"), []byte(c.Markdown("")), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := AutoReport(vault, "shop").Text(); strings.Contains(got, "checkpointed") {
		t.Errorf("resume reported again what auto did before the worktree's checkpoint:\n%s", got)
	}
}
