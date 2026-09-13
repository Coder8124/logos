package legacy

import (
	"fmt"
	"os"
	"path/filepath"
)

// StateDir moves a vault's .brain/ to .logos/ and returns a line saying what it
// did, or "" when there was nothing old there.
//
// Moved rather than read in place: the index, model config, ingest consent and
// web pairing each open .logos/ on their own, and a second name for every one
// of them is the kind of fork that goes wrong in only one. When both exist
// neither is touched, because either could hold the config somebody relies on.
func StateDir(vaultDir string) string {
	old, next := filepath.Join(vaultDir, ".brain"), filepath.Join(vaultDir, ".logos")
	if !isDir(old) {
		return ""
	}
	if _, err := os.Lstat(next); err == nil {
		return fmt.Sprintf("%s is left over from 0.4 and not read; logos uses %s — delete .brain once nothing in it is needed", old, next)
	}
	if err := os.Rename(old, next); err != nil {
		return fmt.Sprintf("could not move %s to %s (%v) — the index will rebuild, but model config and ingest consent stay behind until it is moved", old, next, err)
	}
	return fmt.Sprintf("moved %s to %s", old, next)
}
