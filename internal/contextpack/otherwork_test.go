package contextpack

import (
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/brain/internal/session"
)

// Claude Code left a payments migration half done; a second later Cursor
// checkpointed a CSS fix on the same project, and resume showed only the CSS
// work. The next session was restored to the wrong task and the migration was
// never mentioned again.
func TestANewerCheckpointDoesNotHideOtherOpenWorkOnTheProject(t *testing.T) {
	ix := seedVault(t)
	now := time.Now()
	// Three agents, because the filename is the ordering key and one agent's
	// second checkpoint in the same second is pushed a second forward, past the
	// others.
	for _, c := range []*session.Checkpoint{
		{Project: "kestrel-one", Agent: "claude", Task: "migrate payments to stripe v3",
			Next: "finish webhook signature check", TS: now.Add(-3 * time.Minute).Unix()},
		{Project: "kestrel-one", Agent: "codex", Task: "fix CSS on the landing page",
			Next: "check spacing", TS: now.Add(-2 * time.Minute).Unix()},
		{Project: "kestrel-one", Agent: "cursor", Task: "fix CSS on the landing page",
			Next: "check mobile", TS: now.Add(-time.Minute).Unix()},
	} {
		if err := session.Commit(ix.DB, ix.Vault, c); err != nil {
			t.Fatal(err)
		}
	}
	p, err := Build(ix, nil, "", Request{Task: "continue", Hint: "kestrel-one", Now: now.Unix()})
	if err != nil {
		t.Fatal(err)
	}
	out := p.Render()
	if !strings.Contains(out, "**Other open work on this project:**") ||
		!strings.Contains(out, "migrate payments to stripe v3") ||
		!strings.Contains(out, "finish webhook signature check") {
		t.Errorf("the unfinished migration is not listed:\n%s", out)
	}
	// An earlier checkpoint of the task the latest one carries on is that
	// task's past, not other work.
	if strings.Contains(out, "check spacing") {
		t.Errorf("an earlier checkpoint of the current task is listed as other work:\n%s", out)
	}
}
