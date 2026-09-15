// Package testenv keeps test runs away from the developer's real host CLIs.
package testenv

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// HostCLIs are the host programs setup and doctor execute.
var HostCLIs = []string{"claude", "codex"}

// Run runs the tests with HostCLIs removed from PATH and everything else on it
// still reachable.
//
// Doctor runs `claude mcp list`, and Claude Code health-checks every server it
// knows by starting it — the developer's real Logos plugin included — with the
// test's environment, so a test's LOGOS_VAULT=<temp>/not-a-vault made the
// plugin fail to start. Claude Code caches that failure and skips Logos in
// every new session for 15 minutes: 592 of 638 recorded plugin failures on one
// machine came from `go test ./cmd/...`. Dropping the whole directory from PATH
// would take git and go with it, so each directory holding one of them is
// replaced by a copy made of links to everything else in it.
func Run(m *testing.M) int {
	tmp, err := os.MkdirTemp("", "logos-testpath-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "testenv:", err)
		return 1
	}
	defer os.RemoveAll(tmp)

	var dirs []string
	for i, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if !holdsHostCLI(dir) {
			dirs = append(dirs, dir)
			continue
		}
		shadow := filepath.Join(tmp, fmt.Sprint(i))
		if err := linkAllBut(dir, shadow); err != nil {
			fmt.Fprintln(os.Stderr, "testenv:", err)
			return 1
		}
		dirs = append(dirs, shadow)
	}
	os.Setenv("PATH", strings.Join(dirs, string(os.PathListSeparator)))
	// Setup also finds claude and codex bundled inside the VS Code extension and
	// the Codex app when PATH has none; this is setup's noBundledCLIsEnv.
	os.Setenv("LOGOS_TEST_NO_BUNDLED_CLIS", "1")
	return m.Run()
}

func holdsHostCLI(dir string) bool {
	for _, name := range HostCLIs {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}
	return false
}

func linkAllBut(dir, shadow string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	if err := os.Mkdir(shadow, 0o700); err != nil {
		return err
	}
	skip := map[string]bool{}
	for _, name := range HostCLIs {
		skip[name] = true
	}
	for _, e := range entries {
		if skip[e.Name()] {
			continue
		}
		if err := os.Symlink(filepath.Join(dir, e.Name()), filepath.Join(shadow, e.Name())); err != nil {
			return err
		}
	}
	return nil
}
