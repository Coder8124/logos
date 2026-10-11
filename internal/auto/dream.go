package auto

import (
	"fmt"
	"os"
	"os/exec"
	"time"

	vaultpkg "github.com/Coder8124/logos/internal/vault"
)

// DreamEvery is how long a dream run counts as recent. A started run counts as
// much as a finished one: a dream that is still going, or died, must not be
// launched again by every session start until somebody notices.
const DreamEvery = 24 * time.Hour

// Launch starts a logos command for a vault as a process of its own. A var so
// tests run the work in-process instead of spawning the test binary.
var Launch = launch

func launch(vault string, args ...string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, args...)
	// Pinned to the vault that launched it: the child inherits no cwd-derived
	// guess, and a run into another vault would be logged in neither.
	cmd.Env = append(os.Environ(), "LOGOS_VAULT="+vault)
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// StartDream launches the day's dream when the switch is on and none has
// started in the last DreamEvery, and says whether it did. Session start is
// the trigger because it is the one moment Logos reliably runs; the dream is
// its own process so the session never waits on a model. Under a lock, so two
// hosts starting at once launch one dream between them.
func StartDream(vault string, now time.Time) (bool, error) {
	if !On(vault, Dream) {
		return false, nil
	}
	g, err := vaultpkg.Lock(vault, "auto-dream")
	if err != nil {
		return false, err
	}
	defer g.Unlock()
	recent, err := Entries(vault, "", now.Add(-DreamEvery))
	if err != nil {
		return false, err
	}
	for _, e := range recent {
		if e.Switch == Dream {
			return false, nil
		}
	}
	// Recorded before the launch, so the log is what stops the next session
	// from launching a second one, whatever the child does.
	if err := Record(vault, Entry{At: now, Switch: Dream, Started: true}); err != nil {
		return false, fmt.Errorf("could not record the dream in %s, so it was not started: %w", LogFile, err)
	}
	if err := Launch(vault, "dream", "--auto"); err != nil {
		Record(vault, Entry{At: now, Switch: Dream, Failed: "could not start: " + err.Error()})
		return false, err
	}
	return true, nil
}
