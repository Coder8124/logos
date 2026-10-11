// Package claudemem finds claude-mem's local store. It lives apart from the
// importer so session start can ask whether the store changed without
// depending on the command that reads it.
package claudemem

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DBPath is where claude-mem keeps its store, resolved the way claude-mem
// resolves it (src/shared/paths.ts): CLAUDE_MEM_DATA_DIR, then the same key in
// ~/.claude-mem/settings.json (flat, or under "env"), then ~/.claude-mem.
// Missing a relocated store would report an install as empty.
func DBPath() string {
	home, _ := os.UserHomeDir()
	dir := os.Getenv("CLAUDE_MEM_DATA_DIR")
	if dir == "" {
		dir = filepath.Join(home, ".claude-mem")
		var doc map[string]any
		if raw, err := os.ReadFile(filepath.Join(dir, "settings.json")); err == nil && json.Unmarshal(bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf")), &doc) == nil {
			if env, ok := doc["env"].(map[string]any); ok {
				doc = env
			}
			if d, ok := doc["CLAUDE_MEM_DATA_DIR"].(string); ok && d != "" {
				dir = d
			}
		}
	}
	if dir == "~" {
		dir = home
	} else if strings.HasPrefix(dir, "~/") {
		dir = filepath.Join(home, dir[2:])
	}
	return filepath.Join(dir, "claude-mem.db")
}

// Changed is when the store at path was last written, or zero when there is no
// store. The write-ahead log counts too: a running claude-mem writes there,
// and the main file's time can stay put for as long as it runs.
func Changed(path string) time.Time {
	var at time.Time
	for _, p := range []string{path, path + "-wal"} {
		if fi, err := os.Stat(p); err == nil && fi.ModTime().After(at) {
			at = fi.ModTime()
		}
	}
	if _, err := os.Stat(path); err != nil {
		return time.Time{}
	}
	return at
}
