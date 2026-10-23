package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Coder8124/logos/internal/activity"
	"github.com/Coder8124/logos/internal/scope"
	"github.com/Coder8124/logos/internal/session"
	vaultmod "github.com/Coder8124/logos/internal/vault"
)

// runDeny keeps Logos out of the repository it is run in, or with --undo lets
// it back in. Every repository is tracked unless it says otherwise (1g), so
// this is the one switch a user needs, and it lives in the repository rather
// than in a config they would have to find. See scope.DenyMarker.
func runDeny(args []string) error {
	// Anywhere in the line, not only before the first flag: `--undo <dir>`
	// otherwise undid the working directory and left <dir> off.
	dir := ""
	for _, a := range args {
		if !isFlagToken(a) {
			dir = a
			break
		}
	}
	if dir == "" {
		d, err := os.Getwd()
		if err != nil {
			return err
		}
		dir = d
	}
	root := scope.DenyRoot(dir)

	if hasFlag(args, "--undo") {
		removed, err := scope.Undeny(dir)
		// Reported before the error: a marker removed and then a failure on
		// the next is half an undo, and the half that happened is news.
		for _, p := range removed {
			fmt.Printf("removed %s\n", p)
		}
		if err != nil {
			return fmt.Errorf("%s is still denied: %w", root, err)
		}
		if len(removed) == 0 {
			fmt.Printf("%s was not denied, so there was nothing to undo; Logos tracks it.\n", root)
			return nil
		}
		fmt.Printf("Logos tracks %s again from the next session.\n", root)
		return nil
	}

	if scope.Denied(dir) {
		fmt.Printf("%s is already denied; `logos config deny --undo` lets Logos back in.\n", root)
		return nil
	}
	path, err := scope.Deny(dir)
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	fmt.Printf("Logos will not track %s: from the next session its hooks and MCP server here read and write nothing.\n", root)
	fmt.Printf("  marker: %s — commit it to keep every agent out, or leave it uncommitted to keep it yours; `logos config deny --undo` removes it.\n", path)
	// Said because a user who denies a repo after a week in it will otherwise
	// assume the week went with it.
	fmt.Println("  what the vault already holds for it stays there; `logos forget` removes a memory.")
	return nil
}

// deniedHere is the check every hook entry point makes before it writes. It
// asks every directory that names the repository — the working directory a
// host starts the MCP server in, the project dir it gives its hooks, and any
// the caller read from the payload — because the hooks name the project by the
// latter two, and an agent that cd'd out of a denied repo was logged under the
// directory it went to.
func deniedHere(also ...string) bool {
	dirs := append([]string{os.Getenv("CLAUDE_PROJECT_DIR")}, also...)
	if wd, err := os.Getwd(); err == nil {
		dirs = append(dirs, wd)
	}
	for _, d := range dirs {
		if scope.Denied(d) {
			return true
		}
	}
	return false
}

// activityNotice is what the session-start hook injects: the first-contact
// line for a repository Logos has not seen, and the activity log's one-time
// disclosure. Every failure prints nothing, because a hook is not a place to
// report and a denied repository was promised silence.
func activityNotice(args []string) error {
	dir := ""
	if rest := firstNonFlags(args); len(rest) > 0 {
		dir = rest[0]
	}
	if dir == "" {
		d, err := os.Getwd()
		if err != nil {
			return nil
		}
		dir = d
	}
	if scope.Denied(dir) {
		return nil
	}

	// The vault is made here, as the MCP server makes it, and for the same
	// reason: a plugin installed without setup used to record nothing at all,
	// because every hook found no vault and returned. A vault somebody chose
	// and that is missing is not made — that is a drive not mounted, and
	// requireVault's error is for a command someone typed, not this.
	vault, explicit := vaultmod.Chosen()
	var said []string
	if _, err := os.Stat(vault); err != nil {
		if explicit || vaultmod.MkdirPrivate(vault) != nil {
			return nil
		}
		said = append(said, fmt.Sprintf("Logos created a vault at %s.", vault))
	}

	// Nobody was asked, so the repo-is-tracked default decides: without the
	// activity log there is nothing for the session-end checkpoint to be made
	// from. An explicit off is a decision and is kept. Turning it on here is
	// not silent — Disclose below says so, in this same notice.
	if !activity.Decided(vault) {
		_ = activity.SetRecording(vault, true)
	}

	project := projectFor(dir)
	first := project != "" && firstContact(vault, project)
	if first {
		said = append(said, "Logos started tracking this repo — `logos config deny` to stop.")
	}
	disclosure, _ := activity.Disclose(vault)
	if disclosure != "" {
		said = append(said, disclosure)
	}
	if len(said) == 0 {
		return nil
	}
	fmt.Println(strings.Join(said, " "))

	// Marked only now they have been said, so a crash on the way out says them
	// again next session rather than swallowing them for good.
	if first {
		_ = markTracked(vault, project)
	}
	if disclosure != "" {
		_ = activity.MarkDisclosed(vault)
	}
	return nil
}

// trackedFile lists the projects whose first contact has been announced. In
// the vault beside the activity markers, not in the index: a deleted index
// announcing every repository again is the failure invariant 1 forbids.
func trackedFile(vault string) string {
	return filepath.Join(vault, activity.Dir, ".tracked")
}

// firstContact reports whether Logos has never announced or recorded anything
// for project. A project with checkpoints already is not new, which keeps a
// vault from before this file existed from greeting every repository it has.
func firstContact(vault, project string) bool {
	if _, err := os.Stat(filepath.Join(vault, session.CheckpointDir, project)); err == nil {
		return false
	}
	f, err := os.Open(trackedFile(vault))
	if err != nil {
		return true
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) == project {
			return false
		}
	}
	return true
}

func markTracked(vault, project string) error {
	if err := vaultmod.MkdirPrivate(filepath.Dir(trackedFile(vault))); err != nil {
		return err
	}
	f, err := os.OpenFile(trackedFile(vault), os.O_APPEND|os.O_CREATE|os.O_WRONLY, vaultmod.FileMode)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(f, project); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
