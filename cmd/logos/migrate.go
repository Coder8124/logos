package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Coder8124/logos/internal/setup"
	"github.com/Coder8124/logos/internal/vault"
)

// migrateCmd moves a 0.4 vault from ~/brain to ~/logos.
//
// Through 0.4.x legacy.Vault adopts ~/brain in place, and that keeps working
// past 0.5.0 because the pointer names it — but it leaves an early adopter's
// sessions and checkpoints under the old name for good, and an explicit move is
// the only way off it: logos never moves someone's vault without being asked.
//
// ~/brain is left as a link to ~/logos. Host configs pin the vault by path, a
// server already running holds it open, and a shell profile may export
// BRAIN_VAULT; all of them keep reaching the same files through the link, so a
// host that could not be re-pinned is a thing to fix, not a vault gone missing.
func migrateCmd(args []string) error {
	dryRun, yes := hasFlag(args, "--dry-run"), hasFlag(args, "--yes") || hasFlag(args, "-y")
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	from, to := filepath.Join(home, "brain"), filepath.Join(home, "logos")

	fi, err := os.Lstat(from)
	switch {
	case err == nil && fi.Mode()&os.ModeSymlink != 0:
		dest, _ := filepath.EvalSymlinks(from)
		if real, _ := filepath.EvalSymlinks(to); dest != "" && dest == real {
			fmt.Printf("  vault      already at %s; %s links to it\n", to, from)
			return nil
		}
		return fmt.Errorf("%s is a link, not a vault — nothing to move", from)
	case os.IsNotExist(err):
		return fmt.Errorf("there is no vault at %s to move", from)
	case err != nil:
		return err
	case !fi.IsDir():
		return fmt.Errorf("%s is not a directory — nothing to move", from)
	}
	// The pointer decides which vault this machine uses. Naming another one, it
	// means ~/brain is not in use, and moving it would change nothing anyone
	// reads while announcing that it had.
	if p := vault.Pointer(); p != "" && filepath.Clean(p) != from {
		return fmt.Errorf("this machine's vault is %s, not %s — migrate moves only the 0.4 default, so nothing was moved", p, from)
	}
	// A rename cannot merge two vaults, and os.Rename onto an empty directory
	// succeeds on some systems, which would be the same loss without an error.
	if _, err := os.Lstat(to); err == nil {
		return fmt.Errorf("%s already exists — move or merge it yourself first; nothing was moved", to)
	}
	var pinned []string
	for _, h := range detectHosts() {
		if h.Detect == nil || !h.Detect() {
			continue
		}
		if v := expandHome(setup.PinnedVault([]setup.Host{h}, h.Name)); v != "" && filepath.Clean(v) == from {
			pinned = append(pinned, h.Name)
		}
	}

	fmt.Printf("  move       %s → %s\n", from, to)
	fmt.Printf("  link       %s → %s, so anything still naming the old path finds it\n", from, to)
	fmt.Printf("  record     %s as this machine's vault\n", to)
	for _, n := range pinned {
		fmt.Printf("  re-pin     %s, pinned to %s\n", n, from)
	}
	if dryRun {
		fmt.Println("\n  --dry-run: nothing was changed")
		return nil
	}
	if !yes && !confirmNo("\nmove the vault?") {
		return errors.New("nothing was moved — pass --yes to move it without asking")
	}

	if err := os.Rename(from, to); err != nil {
		return fmt.Errorf("could not move %s to %s: %w — nothing was changed", from, to, err)
	}
	fmt.Printf("\n  ✓ moved    %s → %s\n", from, to)
	linkErr := os.Symlink(to, from)
	if linkErr != nil {
		fmt.Printf("  ✗ link     could not link %s to it: %v — anything still naming %s will not find the vault\n", from, linkErr, from)
	} else {
		fmt.Printf("  ✓ linked   %s → %s\n", from, to)
	}
	if err := vault.Record(to); err != nil {
		// With the link, the old pointer still reaches the vault; without it,
		// the pointer names a path that is gone and every run opens it empty.
		if linkErr != nil {
			return fmt.Errorf("moved the vault to %s but could not record it (%v) or link the old path — run `logos setup --vault %s --move-vault --no-hosts` now", to, err, to)
		}
		fmt.Printf("  ✗ record   could not record %s (%v); the old pointer still reaches it through the link — run `logos setup --vault %s --move-vault --no-hosts`\n", to, err, to)
	} else {
		fmt.Printf("  ✓ recorded %s as this machine's vault\n", to)
	}
	var repinErr error
	if len(pinned) > 0 {
		repinErr = wireHosts(to, wireOpts{only: pinned, yes: true, repin: true})
	}
	fmt.Println("\n  restart any open agent sessions so they pick up the new path")
	if repinErr != nil {
		// The vault moved, so this is not "nothing happened" — but a host left
		// on the old path is a failure, and exiting 0 would hide it.
		return fmt.Errorf("moved the vault to %s, but re-pinning the hosts failed: %w — `logos mcp install` retries it", to, repinErr)
	}
	return nil
}

// migrateHint says, on stderr, that the vault is still the 0.4 default and
// which command moves it. Without it the command exists only for the people
// who read the release notes.
func migrateHint(stderr io.Writer) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	old := filepath.Join(home, "brain")
	if filepath.Clean(vault.Path()) != old {
		return
	}
	if fi, err := os.Lstat(old); err != nil || !fi.IsDir() {
		return
	}
	fmt.Fprintf(stderr, "logos: your vault is still at %s — `logos migrate` moves it to %s\n", old, filepath.Join(home, "logos"))
}
