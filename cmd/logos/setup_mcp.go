package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/Coder8124/logos/internal/selfupdate"
	"github.com/Coder8124/logos/internal/setup"
)

// logosServer is the command line and environment any host — known to
// setup.Hosts() or not — needs to reach this logos and this vault. Shared by
// wireHosts, --print-config and --config so that all three describe the exact
// same server; a hand-typed config that differs from what `logos setup` itself
// would have written is a bug users would have no way to notice.
func logosServer(vault string) (setup.Server, error) {
	bin, err := selfPath()
	if err != nil {
		return setup.Server{}, err
	}
	return serverFor(bin, vault), nil
}

// terminalCommand is how this install is reached from a shell, for the
// commands setup suggests, with a hint when that is not simply `logos`. Setup
// used to say "logos resume <project>" regardless, and under npx, a source
// build or a release binary run from Downloads there is no logos on PATH.
func terminalCommand(self string) (cmd, hint string) {
	switch selfupdate.DetectInstall(self) {
	case selfupdate.NPX:
		return "npx @noeton/logos", "for a `logos` command, run `npm i -g @noeton/logos`"
	case selfupdate.NPMManaged:
		// npm's logos is a node shim, not this file, so it cannot be compared
		// by path; being on PATH is the whole question.
		if _, err := exec.LookPath("logos"); err == nil {
			return "logos", ""
		}
	default:
		if found, err := exec.LookPath("logos"); err == nil {
			if resolved, err := filepath.EvalSymlinks(found); err == nil && resolved == self {
				return "logos", ""
			}
		}
	}
	// Asked of the real path, before the quoting below rewrites it.
	pinned, dir := ourPin(self), filepath.Dir(self)
	// Quoted for both hints, not just the last one: a home directory with a
	// space in it is exactly where a command gets pasted and splits in two.
	// The directory needs it as much as the binary — it is the argument of the
	// other hint, and it is the half that carries the user's name.
	self, dir = shellQuote(self), shellQuote(dir)
	// setup's own copy is where it is on purpose: the hosts are wired to it and
	// the plugin's resolver searches that directory. Telling the user to move
	// it would break both, so the fix is to put the directory on PATH.
	if pinned {
		return self, fmt.Sprintf("add %s to your PATH to type `logos`", dir)
	}
	// The hosts setup just wired launch this exact path, so moving the file
	// breaks every one of them unless setup rewires them to where it went.
	return self, "logos is not on your PATH — to type `logos`, move it into a directory that is (for example ~/.local/bin), then run `logos setup` again: the hosts are wired to where it is now"
}

// probeTarget is what the integration check launches, which is deliberately not
// always what the hosts launch.
//
// Under npx the wired command is `npx -y @noeton/logos mcp serve`, and running
// that here would make `logos doctor` fetch the package whenever npm's cache has
// been pruned — an egress from a command that promises nothing leaves the
// machine, and slow enough that the probe's ten-second handshake deadline
// expires first, reporting a perfectly healthy install as broken. The server
// binary is identical either way; npx only adds the fetch. So probe this binary
// and say out loud that the wired command differs, rather than quietly claiming
// to have tried it.
func probeTarget(self string, srv setup.Server) (bin string, args []string, note string) {
	// Only npx's launcher fetches; an absolute path (this binary, or Homebrew's
	// opt link to it) is probed as written.
	if launchesThroughNpx(srv) {
		return self, []string{"mcp", "serve"},
			fmt.Sprintf("probed this binary; hosts launch `%s %s`, which resolves the same server on demand",
				srv.Bin, strings.Join(srv.Args, " "))
	}
	return srv.Bin, srv.Args, ""
}

// hostOS is runtime.GOOS, a variable so the Windows launcher can be tested on
// the machines this suite actually runs on.
var hostOS = runtime.GOOS

// npxServer is the command a host runs to resolve logos through npx. On
// Windows npx is npx.cmd, a batch file, and a host that spawns "npx" directly
// fails to start it with nothing in its log pointing at why; cmd /c is how
// Windows runs a batch file, and is what Claude Code's docs prescribe (#21).
func npxServer(env map[string]string) setup.Server {
	args := []string{"-y", "@noeton/logos", "mcp", "serve"}
	if hostOS == "windows" {
		return setup.Server{Bin: "cmd", Args: append([]string{"/c", "npx"}, args...), Env: env}
	}
	return setup.Server{Bin: "npx", Args: args, Env: env}
}

// launchesThroughNpx reports whether srv is npxServer's launcher, on either OS.
func launchesThroughNpx(srv setup.Server) bool {
	return srv.Bin == "npx" || (srv.Bin == "cmd" && len(srv.Args) > 1 && srv.Args[0] == "/c" && srv.Args[1] == "npx")
}

// serverFor is the decision logosServer makes, separated from finding this
// process's own path so it can be tested for a binary this test run is not
// executing from.
//
// The README's own install line is `npx -y @noeton/logos setup`, and under npx
// the binary lives in a cache directory npm prunes. Writing that path into a
// host config produces the worst shape of failure this product has: setup says
// "Working", and weeks later the host fails to launch a binary that is simply
// gone, with nothing tying it back to the install. npx resolves a copy on
// demand, so name the command instead of the file — which is also the config
// npm/README.md tells people to write by hand, "portable between machines,
// which an absolute binary path is not".
func serverFor(bin, vault string) setup.Server {
	// Absolute, and always written: a host launches the server from a directory
	// nobody chose, and a relative vault would silently resolve somewhere the
	// user will never look.
	env := map[string]string{"LOGOS_VAULT": vault}
	if selfupdate.DetectInstall(bin) == selfupdate.NPX {
		return npxServer(env)
	}
	// Under Homebrew bin is the versioned Cellar path, which `brew upgrade`
	// deletes; the opt link follows upgrades.
	if stable := selfupdate.HomebrewStablePath(bin); stable != "" {
		bin = stable
	}
	return setup.Server{Bin: bin, Args: []string{"mcp", "serve"}, Env: env}
}

// resolvedVault is the vault --print-config and --config act on: an explicit
// --vault, falling back to the one this machine already has configured. Never
// created here — printing or merging a config is not the step that brings a
// vault into existence, and doing so behind a flag whose whole point is "just
// show me / just write this" would be the same silent-vault-creation mistake
// chooseVault's own doc comment already explains.
func resolvedVault(args []string) (string, error) {
	v := flagStr(args, "--vault", "")
	if v == "" {
		v = vaultPath()
	}
	return filepath.Abs(expandHome(v))
}

// printConfigCmd is `logos setup --print-config`: the server block by hand,
// for an MCP client that is not one of the four Hosts() knows how to find or
// register. Those clients are real — MCP has more of them than this package
// will ever special-case — and until this existed, the only answer for their
// users was silence.
func printConfigCmd(args []string) error {
	vault, err := resolvedVault(args)
	if err != nil {
		return err
	}
	srv, err := logosServer(vault)
	if err != nil {
		return err
	}
	out, err := setup.RenderConfig(srv, flagStr(args, "--format", ""))
	if err != nil {
		return err
	}
	fmt.Print(out)
	return nil
}

// configFileCmd is `logos setup --config <path>`: merge logos into a config
// file at a location logos has no built-in convention for, reusing the exact
// merge (parse-before-touch, backup-before-write, no-op-writes-nothing)
// mergeJSON already gives Claude Desktop and Cursor.
func configFileCmd(args []string, path string) error {
	vault, err := resolvedVault(args)
	if err != nil {
		return err
	}
	srv, err := logosServer(vault)
	if err != nil {
		return err
	}
	abs, err := filepath.Abs(expandHome(path))
	if err != nil {
		return err
	}
	outcome, err := setup.MergeFile(abs, srv)
	if err != nil {
		return err
	}
	fmt.Printf("  %-16s %s (%s)\n", "config", outcome, abs)
	return nil
}

// mcpInstallCmd is the wiring on its own, for someone who already has a vault.
func mcpInstallCmd(args []string) error {
	if hasFlag(args, "--help") || hasFlag(args, "-h") {
		fmt.Print(setupUsage)
		return nil
	}
	args, err := normalizeSetupFlags(args)
	if err != nil {
		return err
	}
	vault := flagStr(args, "--vault", "")
	if vault == "" {
		vault = vaultPath()
	}
	abs, err := filepath.Abs(expandHome(vault))
	if err != nil {
		return err
	}
	if _, err := os.Stat(abs); err != nil {
		return fmt.Errorf("vault not found at %s — run `logos setup` first, or pass --vault", abs)
	}
	return wireHosts(abs, wireOptsFrom(args))
}

// mcpUninstallCmd is `logos mcp uninstall [--host NAME]`, the way back out of
// install. It edits host configs and nothing else: the vault is the user's
// memory, so where it was left is said and deleting it stays their call.
func mcpUninstallCmd(args []string) error {
	args, err := normalizeFlags(args, uninstallValueFlags, uninstallBoolFlags)
	if err != nil {
		return err
	}
	known := detectHosts()
	names := flagStrs(args, "--host")
	hosts, unmatched := setup.Only(known, names)
	if len(unmatched) > 0 {
		return fmt.Errorf("unknown host %s — logos knows: %s",
			strings.Join(unmatched, ", "), strings.Join(setup.Names(known), ", "))
	}
	// The plugin goes with Claude Code and only with it: `--host cursor` is not
	// a run that should take Claude Code's plugin out from under it.
	pluginGoing := hasHost(hosts, "Claude Code") && setup.LogosPluginRecord().Installed
	going := setup.Names(hosts)
	if pluginGoing {
		going = append(going, "the Claude Code plugin")
	}
	// Unwiring more than one thing at a time is asked about, the way wiring them
	// is: `--host` matches on a prefix and a mistyped flag used to mean every
	// host, so the run that takes logos off the whole machine says what it is
	// about to remove before it does it.
	if len(going) > 1 && !hasFlag(args, "--yes") && !hasFlag(args, "-y") {
		fmt.Printf("  %-*s %s\n", hostColumn, "hosts", strings.Join(going, ", "))
		if !confirm("  Remove logos from all of them?") {
			fmt.Println("  nothing was removed")
			return nil
		}
	}
	failed := 0
	removals := setup.Uninstall(hosts)
	if len(removals) == 0 {
		// About the selection, not the machine: "nothing was removed" for a
		// host the user named reads as "already clean", and they leave an entry
		// in place that is still there.
		if len(names) > 0 {
			fmt.Printf("  %-*s %s is not installed here (found: %s), so nothing was removed\n", hostColumn, "hosts",
				strings.Join(setup.Names(hosts), ", "), strings.Join(setup.Names(setup.Detected(known)), ", "))
		} else {
			fmt.Printf("  %-*s none of the hosts logos knows are installed here, so nothing was removed\n", hostColumn, "hosts")
		}
	}
	for _, r := range removals {
		switch {
		case r.Err != nil:
			failed++
			fmt.Printf("  %-*s failed: %v\n", hostColumn, r.Host, r.Err)
		case len(r.Removed) == 0:
			fmt.Printf("  %-*s not registered\n", hostColumn, r.Host)
		default:
			fmt.Printf("  %-*s removed %s  (%s)\n", hostColumn, r.Host, strings.Join(r.Removed, " and "), r.Where)
		}
		// Invariant 3: a hook removed silently is one the user keeps looking for.
		if r.Unhooked {
			fmt.Printf("  %-*s and its session-start hook\n", hostColumn, "")
		}
		if r.Backup != "" {
			fmt.Printf("  %-*s backup of the old config: %s\n", hostColumn, "", r.Backup)
		}
	}
	// The plugin carries its own server, which no host config holds, so it is
	// removed through claude's own CLI rather than left running.
	if pluginGoing {
		switch {
		case !setup.SupportsPluginCommands():
			fmt.Printf("\n  %-*s still installed — this claude cannot remove plugins from the command line, so remove it in Claude Code with /plugin uninstall logos@logos\n", hostColumn, "plugin")
		default:
			if err := setup.RunPluginSteps(setup.UninstallPluginSteps()); err != nil {
				failed++
				fmt.Printf("\n  %-*s could not be removed: %v — remove it in Claude Code with /plugin uninstall logos@logos\n", hostColumn, "plugin", err)
			} else {
				fmt.Printf("\n  %-*s uninstalled with `%s`\n", hostColumn, "plugin", setup.PluginCommand(setup.UninstallPluginSteps()[0]))
			}
		}
	}
	fmt.Printf("\n  %-*s left untouched at %s — delete it yourself if you want the memory gone too\n", hostColumn, "vault", vaultPath())
	if failed > 0 {
		return fmt.Errorf("%d host(s) could not be cleaned — see above", failed)
	}
	return nil
}

// otherLogosEntries names the host's registrations, other than the one setup
// just wrote, that also start logos — matched the way doctor's duplicate
// check matches them, so the two never disagree about what counts.
func otherLogosEntries(h setup.Host) []string {
	if h.List == nil {
		return nil
	}
	regs, err := h.List()
	if err != nil {
		return nil
	}
	var names []string
	for _, r := range regs {
		if r.Name == setup.Name {
			continue
		}
		if strings.Contains(r.Command, "mcp serve") || strings.HasPrefix(r.Name, "plugin:logos:") {
			names = append(names, r.Name)
		}
	}
	sort.Strings(names)
	return names
}
