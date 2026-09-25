package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

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
	// LOGOS_VAULT scopes this run to one vault, and a 0.4 profile's BRAIN_VAULT
	// arrives here as LOGOS_VAULT too. A run scoped elsewhere — the scratch-vault
	// habit — would otherwise pass the pointer check below and move the real one.
	if v := os.Getenv("LOGOS_VAULT"); v != "" && filepath.Clean(expandHome(v)) != from {
		return fmt.Errorf("this run's vault is %s (LOGOS_VAULT), not %s — migrate moves only the 0.4 default, so nothing was moved", v, from)
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
	// Each host is re-registered with the entry it already has and only the
	// vault changed. The binary running migrate is the wrong thing to pin: it
	// may be npx, which asks the registry on every launch, a copy `brew upgrade`
	// never reaches, an older release, or a `go run` build Go deletes on exit.
	// Not through wireHosts either: that is setup, and under one --yes it
	// installed the plugin and copied this binary onto PATH.
	type repin struct {
		host setup.Host
		srv  setup.Server
	}
	type leave struct {
		host setup.Host
		why  string
	}
	var pinned []repin
	var kept []leave
	for _, h := range detectHosts() {
		if h.Detect == nil || !h.Detect() {
			continue
		}
		// 0.4.0 to 0.4.2 pinned BRAIN_VAULT, before the rename; the readers
		// know only LOGOS_VAULT, and legacy reads the old name until 0.5.0.
		vaultOf := func(e setup.Registration) string {
			if e.Vault != "" {
				return e.Vault
			}
			return e.Server.Env["BRAIN_VAULT"]
		}
		isOld := func(e *setup.Registration) bool {
			return e != nil && vaultOf(*e) != "" && filepath.Clean(expandHome(vaultOf(*e))) == from
		}
		var logosE, brainE *setup.Registration
		var others []setup.Registration
		for _, e := range setup.PinnedEntries(h) {
			switch e.Name {
			case setup.Name:
				if logosE == nil {
					logosE = &e
				}
			case setup.OldName:
				if brainE == nil {
					brainE = &e
				}
			default:
				if isOld(&e) {
					others = append(others, e)
				}
			}
		}
		// Install writes the logos entry and removes brain, so those are the
		// two names a re-pin can reach. The logos entry is re-pinned when it
		// is on the old path. Otherwise brain is re-pinned as logos — unless a
		// logos entry exists, pinned elsewhere or following the machine's
		// vault: that is the one the user runs, not ours to overwrite.
		var chosen *setup.Registration
		switch {
		case isOld(logosE):
			chosen = logosE
			// Install's Remove takes brain out whatever it names; one on
			// another vault is somebody's choice, not a leftover of this one.
			if brainE != nil && vaultOf(*brainE) != "" && !isOld(brainE) {
				h.Remove = nil
			}
		case isOld(brainE) && logosE != nil:
			uses := "follows this machine's vault"
			if v := vaultOf(*logosE); v != "" {
				uses = "uses " + v
			}
			kept = append(kept, leave{h, fmt.Sprintf("its logos entry %s, and the 0.4 %s entry beside it still names %s — remove that one by hand if it is not wanted", uses, setup.OldName, from)})
		case isOld(brainE):
			chosen = brainE
		}
		for _, e := range others {
			kept = append(kept, leave{h, fmt.Sprintf("its %q entry names %s, and migrate re-pins only logos and 0.4's %s — change its vault by hand", e.Name, from, setup.OldName)})
		}
		if chosen == nil {
			continue
		}
		srv := chosen.Server
		env := map[string]string{}
		for k, val := range srv.Env {
			env[k] = val
		}
		delete(env, "BRAIN_VAULT")
		env["LOGOS_VAULT"] = to
		srv.Env = env
		pinned = append(pinned, repin{h, srv})
	}

	fmt.Printf("  move       %s → %s\n", from, to)
	fmt.Printf("  link       %s → %s, so anything still naming the old path finds it\n", from, to)
	fmt.Printf("  record     %s as this machine's vault\n", to)
	for _, p := range pinned {
		fmt.Printf("  re-pin     %s, pinned to %s\n", p.host.Name, from)
	}
	for _, k := range kept {
		fmt.Printf("  leave      %s: %s\n", k.host.Name, k.why)
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
	var problems []string
	for _, p := range pinned {
		// Through Install for what setup already gets right on a rewrite: the
		// config is copied aside first, and a 0.4 entry under brain is replaced
		// rather than left running beside logos on the old path. Install writes
		// the entry from its command, arguments and environment, so any other
		// key it carried goes; the backup keeps it.
		r := setup.Install(p.srv, []setup.Host{p.host})[0]
		err := r.Err
		if err == nil && r.Outcome == setup.Failed {
			err = errors.New("the host reported a failure")
		}
		if err != nil {
			fmt.Printf("  ✗ re-pin   %s: %v\n", p.host.Name, err)
			problems = append(problems, fmt.Sprintf("%s is still pinned to %s", p.host.Name, from))
			continue
		}
		fmt.Printf("  ✓ re-pinned %s to %s\n", p.host.Name, to)
		if r.Replaced {
			fmt.Printf("             replaced its 0.4 %s entry\n", setup.OldName)
		}
		if r.Hooked == setup.Registered {
			fmt.Printf("             added its session-start hook, as setup does\n")
		}
		if r.Backup != "" {
			fmt.Printf("             the config as it was is at %s\n", r.Backup)
		}
		if r.HookErr != nil {
			fmt.Printf("  ✗ hook     %s: %v\n", p.host.Name, r.HookErr)
		}
		// Re-pinned either way; what is unknown is whether a 0.4 entry is
		// still there beside it — Claude Code's check runs `claude mcp list`,
		// which fails for reasons that have nothing to do with it.
		if r.ReplaceErr != nil {
			fmt.Printf("  ✗ old entry %s: could not check for or remove its 0.4 %s entry: %v\n", p.host.Name, setup.OldName, r.ReplaceErr)
			problems = append(problems, fmt.Sprintf("%s may still have its 0.4 %s entry", p.host.Name, setup.OldName))
		}
	}
	for _, k := range kept {
		problems = append(problems, fmt.Sprintf("%s was left on %s", k.host.Name, from))
	}
	fmt.Println("\n  restart any open agent sessions so they pick up the new path")
	if len(problems) > 0 {
		// The vault moved, so this is not "nothing happened" — but a host left
		// on the old path is a failure, and exiting 0 would hide it.
		return fmt.Errorf("moved the vault to %s, but %s — see above", to, strings.Join(problems, "; "))
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
