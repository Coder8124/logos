package claims

import (
	"testing"

	"github.com/Coder8124/logos/internal/activity"
)

// The log is read newest first and keeps file order within a second, so the
// first event of the newest second is the session that wrote earlier in it.
// Checking that one would hold a checkpoint up against somebody else's commands.
func TestTheSessionCheckedIsTheOneThatWroteLastInTheSameSecond(t *testing.T) {
	vault := t.TempDir()
	if err := activity.SetRecording(vault, true); err != nil {
		t.Fatal(err)
	}
	for _, e := range []activity.Event{
		{TS: 100, Kind: activity.KindTool, Project: "p", Session: "other", Tool: "Bash", Summary: "Bash: go test ./..."},
		{TS: 100, Kind: activity.KindTool, Project: "p", Session: "mine", Tool: "Bash", Summary: "Bash: go build ./..."},
	} {
		if err := activity.Append(vault, e); err != nil {
			t.Fatal(err)
		}
	}
	r, err := Check(vault, "p", []string{"go test ./... passes"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Session != "mine" || len(r.Unseen) != 1 {
		t.Errorf("checked session %q, unseen %q; want mine, with the test it never ran marked", r.Session, r.Unseen)
	}
}
