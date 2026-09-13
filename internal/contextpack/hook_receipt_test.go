package contextpack

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Coder8124/brain/internal/session"
)

// receiptCounter lifts the awk program out of the SessionStart hook, so the
// test runs the counter the plugin ships rather than a copy that can agree
// with render.go while the real one does not.
func receiptCounter(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "plugin", "hooks", "session-start.sh"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	start := strings.Index(s, "awk '\n")
	end := strings.Index(s, "\n' 2>/dev/null) || carried=")
	if start < 0 || end < start {
		t.Fatal("could not find the receipt counter in session-start.sh")
	}
	return s[start+len("awk '\n") : end]
}

// The receipt is the one line the user sees, and the hook tells the model to
// read the failed approaches first — yet its counter knew every section except
// "Already tried, didn't work", so a handoff whose whole value was a ruled-out
// approach announced itself as carrying nothing but a note.
func TestTheRestoreReceiptCountsEverySectionTheHandoffCarriesRuledOutFirst(t *testing.T) {
	if _, err := exec.LookPath("awk"); err != nil {
		t.Skip("no awk on this machine")
	}
	ix := seedVault(t)
	if err := session.Commit(ix.DB, ix.Vault, &session.Checkpoint{
		Project: "kestrel-one", Agent: "claude",
		Task:     "cut the BOM",
		Verified: []string{"the single-mic BOM clears $118"},
		Blockers: []string{"the tariff table is stale"},
		Failed:   []string{"re-quoting the waveguide — no movement", "dropping the second mic — fails the echo test"},
		Next:     "refresh the tariff table",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.AddNote(ix.DB, "kestrel-one", "claude", "tariff table found"); err != nil {
		t.Fatal(err)
	}
	p, err := Build(ix, nil, "", Request{Task: "continue", Hint: "kestrel-one"})
	if err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("awk", receiptCounter(t))
	cmd.Stdin = strings.NewReader(p.Render())
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("awk: %v", err)
	}
	got := strings.TrimSpace(string(out))
	want := "2 ruled-out approaches, 1 verified fact, 1 known blocker, 1 uncheckpointed note"
	if got != want {
		t.Errorf("receipt = %q\nwant      %q\nfrom the pack:\n%s", got, want, p.Render())
	}
}
