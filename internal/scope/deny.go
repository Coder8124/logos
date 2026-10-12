package scope

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// DenyMarker is the file that tells Logos to stay out of a repository. Every
// repository the plugin sees is tracked unless it carries one (1g): opt-in
// setup meant a user could work for a day in a repo with the plugin installed
// and find nothing recorded, with nothing anywhere saying why.
//
// It lives in the repository, not in Logos's config, because it is a property
// of the repository: it travels with a worktree copy, and a team can commit it
// to keep every member's agent out, or leave it ignored to keep it personal.
const DenyMarker = ".logos/deny"

// DenyRoot is the directory `logos deny` writes its marker in: the repository
// dir is inside, and dir itself outside one. The root rather than dir, so a
// deny typed in a subfolder covers the sessions opened at the top.
func DenyRoot(dir string) string {
	if root := gitRoot(dir); root != "" {
		return root
	}
	return filepath.Clean(strings.TrimSpace(dir))
}

// Denied reports whether dir is inside a repository that asked Logos to stay
// out. Every directory from dir up to its DenyRoot is asked, so a marker
// written at a subfolder that was not a repository at the time still holds.
func Denied(dir string) bool {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return false
	}
	dir = filepath.Clean(dir)
	root := DenyRoot(dir)
	for i := 0; i < maxWalk; i++ {
		if _, err := os.Stat(filepath.Join(dir, DenyMarker)); err == nil {
			return true
		}
		parent := filepath.Dir(dir)
		if dir == root || parent == dir {
			break
		}
		dir = parent
	}
	return false
}

// Deny writes the marker at dir's DenyRoot and returns where it went.
func Deny(dir string) (string, error) {
	path := filepath.Join(DenyRoot(dir), DenyMarker)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	// The sentence is for whoever finds the file in a diff and wonders.
	body := "Logos does not track this repository. `logos deny --undo` removes this file and resumes.\n"
	return path, os.WriteFile(path, []byte(body), 0o644)
}

// Undeny removes every marker that makes dir denied, and returns the paths it
// removed — none when it was not denied. All of them, not just the root's: a
// --undo that left one standing in a subfolder would report success on a repo
// that is still off.
func Undeny(dir string) ([]string, error) {
	dir = filepath.Clean(strings.TrimSpace(dir))
	root := DenyRoot(dir)
	var removed []string
	for i := 0; i < maxWalk; i++ {
		path := filepath.Join(dir, DenyMarker)
		if err := os.Remove(path); err == nil {
			removed = append(removed, path)
			// The .logos/ deny made, gone with it when nothing else is there;
			// a vault's .logos/ is never empty, so this cannot touch one.
			_ = os.Remove(filepath.Dir(path))
		} else if !errors.Is(err, fs.ErrNotExist) {
			return removed, err
		}
		parent := filepath.Dir(dir)
		if dir == root || parent == dir {
			break
		}
		dir = parent
	}
	return removed, nil
}
