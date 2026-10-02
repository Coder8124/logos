package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Coder8124/logos/internal/selfupdate"
)

// goRunBinary reports a binary inside a go-build directory, where `go run`
// puts the executable it removes on exit. A `.test` binary lives there too,
// but it is this package's tests calling setup, not a person wiring hosts.
func goRunBinary(bin string) bool {
	if strings.HasSuffix(bin, ".test") || strings.HasSuffix(bin, ".test.exe") {
		return false
	}
	return inGoBuildDir(bin)
}

// inGoBuildDir reports a binary Go built into its own temp tree, `go run`'s and
// the test binary's alike. Neither is an install, so neither is something to
// copy onto a PATH and wire hosts to.
func inGoBuildDir(bin string) bool {
	for _, part := range strings.Split(filepath.ToSlash(bin), "/") {
		if strings.HasPrefix(part, "go-build") {
			return true
		}
	}
	return false
}

// looksLikeSourceTree reports a directory holding a Go or npm project and no
// Logos history. A vault with sessions or memories is a vault whatever else is
// in it; a plain git repository is not enough, since notes vaults are often
// kept in git.
func looksLikeSourceTree(dir string) bool {
	for _, d := range []string{"sessions", "memories"} {
		if _, err := os.Stat(filepath.Join(dir, d)); err == nil {
			return false
		}
	}
	for _, f := range []string{"go.mod", "package.json"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			return true
		}
	}
	return false
}

// executable is os.Executable, replaceable so a test can be a binary running
// from npm's npx cache.
var executable = os.Executable

func selfPath() (string, error) {
	bin, err := executable()
	if err != nil {
		return "", fmt.Errorf("could not find my own path, which the host config needs: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(bin); err == nil {
		bin = resolved
	}
	return bin, nil
}

// offerPathCopy offers to copy a logos that cannot be typed into ~/.local/bin,
// and returns where the copy goes for the hosts to be wired to. "" means wire
// this binary where it stands — the offer was declined, refused, or never
// needed. The copy itself is made at the one place that makes it, alongside
// npx's, so the version guard there covers this route too.
//
// Only asked when the binary is not reachable as `logos`: an install that is
// already on PATH has nothing to move. Excluded are npx, because the caller has
// its own copy to make on a different reason; `go run`, because Go deletes that
// file on exit and setup refuses it a few lines further down anyway; and
// Homebrew's and npm's own installs, because a copy of a managed install is
// frozen at today's version and sits ahead of its manager on PATH, so the hosts
// launch the one logos `brew upgrade` and `npm update -g` can never reach —
// the trap #83 is about.
func offerPathCopy(yes, dryRun bool) (dst, src string) {
	self, err := selfPath()
	if err != nil || inGoBuildDir(self) {
		return "", ""
	}
	switch selfupdate.DetectInstall(self) {
	case selfupdate.NPX, selfupdate.Homebrew, selfupdate.NPMManaged:
		return "", ""
	}
	if _, hint := terminalCommand(self); hint == "" {
		return "", ""
	}
	dst, err = pinnedBinary()
	if err != nil {
		return "", ""
	}
	// Setup run from the copy it made earlier, with that directory still not on
	// PATH, reaches here about the file it is already running as — and offered
	// to copy it onto itself. The hint terminalCommand gave is the right one;
	// there is simply nothing to copy.
	if dst == self {
		return "", ""
	}
	fmt.Printf("\n  logos      %s is not on your PATH, so `logos` is not a command yet\n", self)
	// A plan that leaves out the one file the run creates is not the plan: the
	// roster used to show the hosts pointed at ~/Downloads with nothing saying
	// a real run writes a binary into ~/.local/bin and wires them there. The
	// destination is returned under --dry-run too, so the roster names the
	// binary a real run would wire; the copy itself is behind the dry-run
	// return further up, and is not made.
	if dryRun {
		fmt.Printf("             a real run offers to copy it to %s and wire the hosts to the copy\n", dst)
		return dst, self
	}
	if !yes && !confirm(fmt.Sprintf("             copy it to %s and wire the hosts to the copy?", dst)) {
		return "", ""
	}
	return dst, self
}

// pinnedBinary is where an npx setup keeps its copy of logos.
func pinnedBinary() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	name := "logos"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(home, ".local", "bin", name), nil
}

// pinBinary copies self to dst. Re-running setup through npx refreshes the
// copy, but a file there that is not logos belongs to someone else and is left
// alone. The copy is written beside dst and renamed over it, so a host
// starting mid-copy never launches half a binary.
func pinBinary(self, dst string) error {
	if _, err := os.Stat(dst); err == nil && !runsAsLogos(dst) {
		return fmt.Errorf("%s already exists and is not logos", dst)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	data, err := os.ReadFile(self)
	if err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.tmp-%d", dst, os.Getpid())
	if err := os.WriteFile(tmp, data, 0o755); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return err
	}
	return writePinReceipt(dst)
}

// The receipt beside the copy, naming it. Nothing on disk used to say that the
// logos in ~/.local/bin was setup's own doing, so a later setup could neither
// prefer the install that replaced it nor offer to clear it away — it could
// only tell the user about a file and leave them to judge whose it was (#83).
func pinReceipt() (string, error) {
	dst, err := pinnedBinary()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(dst), ".logos-pin"), nil
}

func writePinReceipt(pin string) error {
	path, err := pinReceipt()
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(pin+"\n"), 0o644)
}

// ourPin reports whether the logos at path is the copy setup made. A logos
// somebody else put in that directory has no receipt, and is not setup's to
// prefer against, replace or remove.
func ourPin(path string) bool {
	receipt, err := pinReceipt()
	if err != nil {
		return false
	}
	data, err := os.ReadFile(receipt)
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(data)) == path
}

// offerPinRemoval offers to take back the copy setup pinned into ~/.local/bin,
// once a managed install — Homebrew's or npm's — is the one running setup.
// That copy is ahead of both on PATH, so leaving it there is what makes `brew
// upgrade` and `logos update` reach an install no host launches. Only offered,
// never assumed: --yes is not an answer to a question about deleting a file.
func offerPinRemoval(self string) {
	switch selfupdate.DetectInstall(self) {
	case selfupdate.Homebrew, selfupdate.NPMManaged:
	default:
		return
	}
	dst, err := pinnedBinary()
	if err != nil || dst == self || !ourPin(dst) {
		return
	}
	if !confirm(fmt.Sprintf("  → remove %s, the copy setup pinned there?", dst)) {
		return
	}
	if err := os.Remove(dst); err != nil {
		fmt.Printf("  could not remove %s: %v\n", dst, err)
		return
	}
	if receipt, err := pinReceipt(); err == nil {
		os.Remove(receipt)
	}
	fmt.Printf("  removed %s\n", dst)
}

// runsAsLogos is the resolver's test in plugin/bin/resolve.sh: every logos
// answers --version with "logos …". Bounded, because the file being asked
// may be any program at all.
func runsAsLogos(path string) bool {
	_, ok := logosVersion(path)
	return ok
}

// logosVersion is the version a logos at path reports, from the same
// `--version` answer runsAsLogos trusts.
func logosVersion(path string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").Output()
	if err != nil || !strings.HasPrefix(string(out), "logos ") {
		return "", false
	}
	fields := strings.Fields(string(out))
	if len(fields) < 2 {
		return "", true
	}
	return fields[1], true
}

// newerRelease reports whether a is a later release than b. Anything that is
// not a plain major.minor.patch — a dev build, an empty answer — compares as
// not newer, so an unreadable version never blocks a copy.
func newerRelease(a, b string) bool {
	pa, okA := releaseParts(a)
	pb, okB := releaseParts(b)
	if !okA || !okB {
		return false
	}
	for i := range pa {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

func releaseParts(v string) ([3]int, bool) {
	var parts [3]int
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	fields := strings.Split(v, ".")
	if len(fields) != 3 {
		return parts, false
	}
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil || n < 0 {
			return parts, false
		}
		parts[i] = n
	}
	return parts, true
}
