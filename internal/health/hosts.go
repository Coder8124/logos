package health

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Coder8124/logos/internal/buildinfo"
	"github.com/Coder8124/logos/internal/setup"
)

// Hosts is the difference between "logos is installed" and "your agents can
// reach it", which are not the same thing and were never distinguished.
func checkHosts() Check {
	var wired []string
	for _, r := range setup.Plan(setup.Hosts()) {
		if r.Outcome == setup.Pending {
			wired = append(wired, r.Host)
		}
	}
	return hostsCheck(wired)
}

// hostsCheck is checkHosts's message-building split out from its detection,
// so the wording can be tested against a chosen list of detected hosts rather
// than whatever happens to be on the machine running the test.
//
// setup.Hosts() is a closed, curated list (see internal/setup's
// package doc) — never the whole set of MCP clients that exist. Every host
// this check names still leaves an open question about the ones it does not
// know, so both branches point at `logos setup --print-config`: the one
// answer that works regardless of which client the user is actually running.
func hostsCheck(wired []string) Check {
	c := Check{Name: "agent hosts"}
	if len(wired) == 0 {
		c.State = Unknown
		c.Detail = "no MCP hosts detected on this machine"
		c.Fix = "install an MCP host such as Claude Code, Cursor, Codex, Cline, Devin or GitHub Copilot, then run `logos mcp install` " +
			"— or run `logos setup --print-config` to wire any other MCP client by hand"
		return c
	}
	// Detected is not the same as wired — Plan reports what is installed, not
	// what points at logos. Say what was actually established.
	c.State = OK
	c.Detail = "detected: " + strings.Join(wired, ", ")
	c.Fix = "run `logos doctor --integration` to prove they can reach this vault" +
		"; for any other MCP client, `logos setup --print-config`"
	return c
}

// A binary registered twice pays its fixed per-session cost twice. This is not
// hypothetical: it doubled a real measured session from ~7,759 to ~13,161
// tokens, the one configuration that breached the 10k-token ceiling on
// unmodified code (see the memory architecture plan's Step 0).
//
// Detection leans on logos's own signature rather than a path comparison: every
// registration setup writes invokes "mcp serve" (setup.go's Server.Args), so
// two entries under one host whose command both contain that phrase are the
// same binary reached two ways. The plugin is the exception that shipped: its
// launcher is bare `bin/mcp.sh` and types "mcp serve" inside the script, so a
// plugin next to a setup registration — the most common duplicate, since the
// README offers both routes — passed as healthy. The plugin is recognised by
// the name Claude Code gives it instead.
func checkDuplicateRegistration(hosts []setup.Host) Check {
	c := Check{Name: "duplicate registration"}
	checked := false
	for _, h := range hosts {
		if h.List == nil || h.Detect == nil || !h.Detect() {
			continue
		}
		regs, err := h.List()
		if err != nil {
			continue
		}
		checked = true
		var dupes []string
		for _, r := range regs {
			if strings.Contains(r.Command, "mcp serve") || strings.HasPrefix(r.Name, "plugin:logos:") {
				dupes = append(dupes, r.Name)
			}
		}
		if len(dupes) > 1 {
			c.State = Failed
			c.Detail = fmt.Sprintf("%s has logos registered %d times: %s", h.Name, len(dupes), strings.Join(dupes, ", "))
			c.Fix = "remove all but one of these entries — each one pays the fixed per-session cost again"
			return c
		}
	}
	if !checked {
		c.State = Unknown
		c.Detail = "no host exposed a readable registration list"
		return c
	}
	c.State = OK
	return c
}

// checkCachedRegistration finds a host launching logos from inside npm's npx
// cache. `npx … setup` on 0.4.2 wired that path, every check passed, and weeks
// later npm pruned the file and the host could not start the server, with
// nothing tying it back to setup. ok is false when no such entry exists.
func checkCachedRegistration(hosts []setup.Host) (Check, bool) {
	for _, h := range hosts {
		if h.List == nil || h.Detect == nil || !h.Detect() {
			continue
		}
		regs, err := h.List()
		if err != nil {
			continue
		}
		for _, r := range regs {
			if !strings.Contains(r.Command, "mcp serve") {
				continue
			}
			if strings.Contains(r.Command, "/_npx/") || strings.Contains(r.Command, `\_npx\`) {
				return Check{
					Name:   "host command",
					State:  Failed,
					Detail: fmt.Sprintf("%s runs %s from npm's npx cache, which npm deletes when it prunes", h.Name, r.Name),
					Fix:    "run `npx -y @noeton/logos setup` again — it registers a command that does not live in the cache",
				}, true
			}
		}
	}
	return Check{}, false
}

// checkMissingRegistration finds a host launching logos from a path that no
// longer exists. Setup wires hosts to the binary it was run as, so a release
// binary run from Downloads and then moved onto PATH left every host pointing
// at nothing, while doctor said the hosts were fine. Only absolute paths are
// checked: `npx …` or a bare `logos` is resolved at launch, not a file here.
// ok is false when every registration's binary exists.
func checkMissingRegistration(hosts []setup.Host) (Check, bool) {
	for _, h := range hosts {
		if h.List == nil || h.Detect == nil || !h.Detect() {
			continue
		}
		regs, err := h.List()
		if err != nil {
			continue
		}
		for _, r := range regs {
			bin, _, ok := strings.Cut(r.Command, " mcp serve")
			if !ok || !filepath.IsAbs(bin) {
				continue
			}
			if _, err := os.Stat(bin); err != nil && os.IsNotExist(err) {
				return Check{
					Name:   "host command",
					State:  Failed,
					Detail: fmt.Sprintf("%s runs %s from %s, which no longer exists", h.Name, r.Name, bin),
					Fix:    "run `logos setup` again from where logos is now — it rewires the hosts to that path",
				}, true
			}
		}
	}
	return Check{}, false
}

// checkOtherVault finds a host pinned to a vault other than the one recorded
// for this machine. `logos setup --vault B --host cursor` moved the record to B
// and left the other hosts on A, so a handoff between them silently split and
// doctor still listed every host as fine. ok is false when there is no
// recorded vault or every host that shows its vault is on it.
func checkOtherVault(hosts []setup.Host, recorded string) (Check, bool) {
	if recorded == "" {
		return Check{}, false
	}
	names, vaults := setup.OnOtherVault(hosts, recorded)
	if len(names) == 0 {
		return Check{}, false
	}
	return Check{
		Name:   "hosts on another vault",
		State:  Failed,
		Detail: fmt.Sprintf("%s uses %s, but this machine's vault is %s — checkpoints there are not seen here", names[0], vaults[0], recorded),
		Fix:    "run `logos mcp install` to point every host at " + recorded,
	}, true
}

// checkOldHostPin finds a host whose logos entry pins its vault as
// BRAIN_VAULT, the name 0.4.0 to 0.4.2 wrote. 0.5.0 does not read it, so that
// host's server opens the machine's vault — or an empty ~/logos — and the only
// thing that says so is a line on the MCP server's stderr, which no host shows.
// Doctor is where someone looks when their memory seems gone. ok is false when
// no entry pins the old name.
func checkOldHostPin(hosts []setup.Host) (Check, bool) {
	for _, h := range hosts {
		if h.Detect == nil || !h.Detect() {
			continue
		}
		for _, e := range setup.PinnedEntries(h) {
			old := e.Server.Env["BRAIN_VAULT"]
			if old == "" || e.Vault != "" {
				continue
			}
			return Check{
				Name:   "host on a 0.4 pin",
				State:  Failed,
				Detail: fmt.Sprintf("%s starts logos with BRAIN_VAULT=%s, which is not read since 0.5.0 — that host is not using %s", h.Name, old, old),
				Fix:    fmt.Sprintf("run `logos setup --vault %s` to re-pin it as LOGOS_VAULT", old),
			}, true
		}
	}
	return Check{}, false
}

// checkPlugin compares the Logos plugin installed in Claude Code with this
// binary. Claude Code does not update a third-party marketplace by default and
// `logos update` replaces only the binary, so a plugin installed early keeps
// running its old hooks against a new server indefinitely — a machine sat on
// 0.1.2 against 0.4.2 with nothing saying so. ok is false when no plugin is
// installed: most people running doctor never used it.
func checkPlugin(version string) (c Check, ok bool) {
	c = CheckPlugin(version)
	return c, c.Name != ""
}

// CheckPlugin is checkPlugin for setup, which skips Claude Code on the
// plugin's account and so owes the same warning. A zero Check means no plugin
// connects Claude Code.
func CheckPlugin(version string) (c Check) {
	r := setup.LogosPluginRecord()
	if !r.Installed {
		return Check{}
	}
	c = Check{Name: "Claude Code plugin"}
	// An installed plugin that Claude Code never loads — turned off in
	// /plugin, or installed for one project — runs no hooks, so nothing
	// restores the last checkpoint when a session starts, and doctor used to
	// say nothing at all about it.
	if !r.Connects {
		c.State = Warn
		// A project's own settings can still enable it, and they cannot be
		// read from here, so the claim is hedged the way setup hedges it.
		c.Detail = "the Logos plugin is " + r.Why + ", so unless a project enables it no session starts with your last checkpoint"
		c.Fix = "enable it for your user in Claude Code's /plugin, then `claude mcp remove --scope user logos` so logos is not registered twice"
		return c
	}
	// An installed, enabled, current plugin whose server Claude Code is
	// refusing to start looks perfect to every other check here, and the line
	// Claude Code prints about it scrolls past at session start. Until the
	// window is out, this machine has no Logos in any new session.
	if left, skipped := setup.PluginConnectionSkipped(time.Now()); skipped {
		c.State = Warn
		c.Detail = fmt.Sprintf("Claude Code cached a failed start of the Logos plugin's server and is skipping it for another %s", left.Round(time.Second))
		c.Fix = "reconnect with /mcp in Claude Code, or start a session after that — and make sure LOGOS_VAULT is unset or right, since a start against the wrong vault is what caches this"
		return c
	}
	pluginVersion := r.Version
	stale, ranked := buildinfo.Older(pluginVersion, version)
	if !ranked {
		c.State = Unknown
		c.Detail = fmt.Sprintf("plugin %q installed; this logos (%s) has no release number to compare it with", pluginVersion, version)
		return c
	}
	if stale {
		c.State = Warn
		c.Detail = fmt.Sprintf("the Logos plugin is %s but this logos is %s — its hooks are older than the server", pluginVersion, strings.TrimPrefix(version, "v"))
		c.Fix = "run `claude plugin marketplace update logos && claude plugin update logos@logos`, then restart Claude Code"
		return c
	}
	c.State = OK
	c.Detail = "plugin " + pluginVersion
	return c
}
