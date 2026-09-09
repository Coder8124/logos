package contextpack

import (
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/brain/internal/session"
)

// An AutoClosed checkpoint — session.CloseAbandoned's output — must say so in
// the render, not just carry a field nothing displays. Losing this clause
// would leave an arriving agent treating a session that simply went silent as
// if the previous agent had deliberately wrapped up.
func TestAutoClosedCheckpointSaysSoInTheRender(t *testing.T) {
	ix := seedVault(t)

	old := time.Now().Add(-2 * session.AbandonAfter).Unix()
	n, err := session.AddNoteAt(ix.DB, "kestrel-one", "claude", "died mid-task", old)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.CloseAbandoned(ix.DB, ix.Vault, n.Session); err != nil {
		t.Fatal(err)
	}

	p, err := Build(ix, nil, "", Request{Task: "continue", Hint: "kestrel-one"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Checkpoint == nil || !p.Checkpoint.AutoClosed {
		t.Fatal("expected an AutoClosed checkpoint to be picked up")
	}
	out := p.Render()
	if !strings.Contains(out, "closed automatically") {
		t.Errorf("render should plainly say this was closed automatically, not by the agent, got:\n%s", out)
	}
}
