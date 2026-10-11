package auto

import (
	"fmt"
	"time"

	vaultpkg "github.com/Coder8124/logos/internal/vault"
)

// StartImport launches `logos import --from claude-mem --auto` when the switch
// is on and claude-mem's store changed after the last automatic import began,
// and says whether it did. changed is the store's last write, zero when there
// is no store.
//
// Measured from when the last run began, not ended: whatever claude-mem wrote
// while it ran may have been missed, and the price of a second look is only
// the skipped count. The log is the record, so deleting the index does not
// set off a run, and under a lock, so two hosts starting at once launch one.
func StartImport(vault string, changed, now time.Time) (bool, error) {
	if changed.IsZero() || !On(vault, Import) {
		return false, nil
	}
	g, err := vaultpkg.Lock(vault, "auto-import")
	if err != nil {
		return false, err
	}
	defer g.Unlock()
	all, err := Entries(vault, "", time.Time{})
	if err != nil {
		return false, err
	}
	// The log is stamped to the second, rounded down, so a change in the same
	// second as the last start reads as after it: a run too many costs a skip
	// count, a run too few loses what claude-mem learned.
	var last time.Time
	for _, e := range all {
		if e.Switch == Import && e.Started {
			last = e.At
		}
	}
	if !last.IsZero() && !changed.After(last) {
		return false, nil
	}
	if err := Record(vault, Entry{At: now, Switch: Import, Started: true}); err != nil {
		return false, fmt.Errorf("could not record the import in %s, so it was not started: %w", LogFile, err)
	}
	if err := Launch(vault, "import", "--from", "claude-mem", "--auto"); err != nil {
		Record(vault, Entry{At: now, Switch: Import, Failed: "could not start: " + err.Error()})
		return false, err
	}
	return true, nil
}
