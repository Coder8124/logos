package health

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Coder8124/logos/internal/selfupdate"
)

// HomebrewPrefixes are where a Homebrew logos is looked for: HOMEBREW_PREFIX,
// which `brew shellenv` sets, then brew's defaults on Apple silicon, Intel
// macOS and Linux. A variable so a test never finds the machine's real brew.
var HomebrewPrefixes = func() []string {
	prefixes := []string{"/opt/homebrew", "/usr/local", "/home/linuxbrew/.linuxbrew"}
	if p := os.Getenv("HOMEBREW_PREFIX"); p != "" {
		prefixes = append([]string{p}, prefixes...)
	}
	return prefixes
}

// CheckOtherInstall finds a second logos that runs instead of this one. An npx
// setup pins a copy in ~/.local/bin, which Claude Code's installer puts ahead
// of Homebrew on PATH, so after a later `brew install` the shell, setup and so
// every host kept running the old copy, `brew upgrade` reached nothing that
// ran, and nothing said there were two. A zero Check means none was found.
func CheckOtherInstall(self, version string) Check {
	c := Check{Name: "logos installs", State: Warn}
	version = strings.TrimPrefix(version, "v")
	switch selfupdate.DetectInstall(self) {
	case selfupdate.Homebrew:
		found, err := exec.LookPath("logos")
		if err != nil {
			return Check{}
		}
		if resolved, err := filepath.EvalSymlinks(found); err != nil || resolved == self {
			return Check{}
		}
		theirs, ok := logosVersion(found)
		if !ok {
			return Check{}
		}
		brew := self
		if stable := selfupdate.HomebrewStablePath(self); stable != "" {
			brew = stable
		}
		c.Detail = fmt.Sprintf("`logos` in a shell runs %s (logos %s), not Homebrew's logos %s — setup run as `logos` wires hosts to that copy, and `brew upgrade` never reaches it", found, theirs, version)
		c.Fix = fmt.Sprintf("remove %s if it is an old copy, then run `%s setup` again", found, brew)
		return c
	case selfupdate.Standalone:
		if opt, theirs := HomebrewInstall(self); opt != "" {
			c.Detail = fmt.Sprintf("Homebrew's logos %s is installed at %s, but this is %s (logos %s) — hosts wired by setup from here launch this copy, which `brew upgrade` never reaches", theirs, opt, self, version)
			c.Fix = fmt.Sprintf("remove %s if it is an old copy, then run `%s setup` again", self, opt)
			return c
		}
	}
	return Check{}
}

// HomebrewInstall is the logos Homebrew has installed, and its version, or ""
// when there is none other than self. The opt path is returned rather than the
// Cellar one it links to: that link is what survives an upgrade, so it is the
// path a host config should name.
func HomebrewInstall(self string) (path, version string) {
	for _, prefix := range HomebrewPrefixes() {
		opt := filepath.Join(prefix, "opt", selfupdate.HomebrewFormula, "bin", "logos")
		if resolved, err := filepath.EvalSymlinks(opt); err != nil || resolved == self {
			continue
		}
		if v, ok := logosVersion(opt); ok {
			return opt, v
		}
	}
	return "", ""
}

// logosVersion is what a logos at path answers to --version. Bounded, because
// the file may be any program with that name.
func logosVersion(path string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").Output()
	fields := strings.Fields(string(out))
	if err != nil || len(fields) < 2 || fields[0] != "logos" {
		return "", false
	}
	return strings.TrimPrefix(fields[1], "v"), true
}
