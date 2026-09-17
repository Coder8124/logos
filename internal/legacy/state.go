package legacy

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
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
		// "Delete it once nothing needs it" is the wrong instruction if something
		// is still writing there. An older logos on the PATH, or a plugin cache
		// pinned to a 0.1.x build, indexes into .brain while everything reads
		// .logos — so the work lands in a cache nothing will ever look in, and
		// the vault is the only reason it is not lost. Telling someone to delete
		// the newer directory hides that; naming the writer is the whole fix.
		if newer(old, next) {
			return fmt.Sprintf("%s is from 0.4 and not read, but it is still being written — something older than this logos is writing there while logos reads %s; find it (`which -a logos`, and any plugin or editor with its own copy) before deleting anything", old, next)
		}
		return fmt.Sprintf("%s is left over from 0.4 and not read; logos uses %s — delete .brain once nothing in it is needed", old, next)
	}
	if err := os.Rename(old, next); err != nil {
		return fmt.Sprintf("could not move %s to %s (%v) — the index will rebuild, but model config and ingest consent stay behind until it is moved", old, next, err)
	}
	return fmt.Sprintf("moved %s to %s", old, next)
}

// newer reports whether anything in a was written more recently than everything
// in b. Top level only: the index, its WAL and the config files all sit there.
func newer(a, b string) bool {
	at, aok := newestWrite(a)
	bt, bok := newestWrite(b)
	return aok && bok && at.After(bt)
}

func newestWrite(dir string) (time.Time, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return time.Time{}, false
	}
	var newest time.Time
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
	}
	return newest, !newest.IsZero()
}
