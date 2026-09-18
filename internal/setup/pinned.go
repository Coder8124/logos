package setup

import "strings"

// PinnedVault returns the LOGOS_VAULT a named host has wired onto its logos
// server, or "" when there is none to read.
//
// #95: setup writes the vault twice — into each host's config as LOGOS_VAULT,
// and once for the machine as the recorded pointer. A tool call arrives through
// the host and gets the pin; the plugin's hooks run with a bare environment and
// get the pointer. When those two disagree the same session restores from one
// vault and checkpoints into another, each side silently consistent with
// itself, and the user reports that their work has stopped being saved. The pin
// is the more specific answer for anything running inside that host, so this is
// how such a caller asks for it instead of re-resolving.
//
// Only a logos entry counts. Other MCP servers set environment of their own,
// and taking one for the vault would point logos at somebody else's directory.
// An unreadable host, an unknown name and a config with no environment in it
// are all "" — this is a question a host may simply not be able to answer,
// which is not a failure.
//
// The answer comes off the config file, never from running the host's own
// listing command. `claude mcp list` prints name, command and status and no
// environment, so it could not answer this anyway — and it is a Node process
// that connects to every server it lists. Asking it here, before argument
// dispatch, on a binary the plugin's per-tool-call hooks run, meant a logos
// started by the plugin started a claude that started a logos.
func PinnedVault(hosts []Host, name string) string {
	for _, h := range hosts {
		if !sameHost(h.Name, name) {
			continue
		}
		if h.Detect != nil && !h.Detect() {
			continue
		}
		if h.Config != nil {
			return pinInConfig(h.Config())
		}
	}
	return ""
}

// pinInConfig reads the pin out of the config file the host keeps its servers
// in — the one place a host's environment is actually written down.
//
// Claude Code is the host the plugin's hooks run inside, and reading its file
// is the only way to see the pin: `claude mcp list` prints name, command and
// status and no environment at all. Both spellings
// of the server map are tried because VS Code calls it "servers" where everyone
// else calls it "mcpServers". A file that is missing, unparseable or somebody
// else's shape is "nothing to say" — the caller keeps the machine pointer.
func pinInConfig(path string) string {
	if path == "" {
		return ""
	}
	if strings.HasSuffix(path, ".toml") {
		regs, err := readCodexServers(path)
		if err != nil {
			return ""
		}
		return logosPin(regs)
	}
	for _, root := range []string{"mcpServers", "servers"} {
		regs, err := readServerBlock(path, root)
		if err != nil {
			continue
		}
		if v := logosPin(regs); v != "" {
			return v
		}
	}
	return ""
}

// logosPin picks the vault off the logos entry among a host's registrations.
func logosPin(regs []Registration) string {
	for _, r := range regs {
		if isLogosServer(r.Command) && r.Vault != "" {
			return r.Vault
		}
	}
	return ""
}

// sameHost compares host names the way a person means them, because the hooks
// pass their host in a shell variable where "Claude Code" is the awkward
// spelling and "claude-code" the natural one. One thing, two spellings.
func sameHost(a, b string) bool {
	fold := func(s string) string {
		return strings.ToLower(strings.NewReplacer(" ", "", "-", "", "_", "").Replace(s))
	}
	return fold(a) == fold(b)
}
