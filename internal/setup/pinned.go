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
// An unreadable host, an unknown name and a listing with no environment are all
// "" — this is a question a host may simply not be able to answer (`claude mcp
// list` does not show environment), which is not a failure.
func PinnedVault(hosts []Host, name string) string {
	for _, h := range hosts {
		if !sameHost(h.Name, name) {
			continue
		}
		if h.Detect != nil && !h.Detect() {
			continue
		}
		if h.List != nil {
			regs, err := h.List()
			if err == nil {
				for _, r := range regs {
					if isLogosServer(r.Command) && r.Vault != "" {
						return r.Vault
					}
				}
			}
		}
		if h.Config != nil {
			return pinInConfig(h.Config())
		}
	}
	return ""
}

// pinInConfig reads the pin out of the config file the host keeps its servers
// in, for the hosts whose listing cannot report environment.
//
// Claude Code is exactly that host, and the one the plugin's hooks run inside:
// `claude mcp list` prints name, command and status and no environment at all,
// so without this #95 stays open on the host it matters most on. Both spellings
// of the server map are tried because VS Code calls it "servers" where everyone
// else calls it "mcpServers". A file that is missing, unparseable or somebody
// else's shape is "nothing to say" — the caller keeps the machine pointer.
func pinInConfig(path string) string {
	if path == "" {
		return ""
	}
	for _, root := range []string{"mcpServers", "servers"} {
		regs, err := readServerBlock(path, root)
		if err != nil {
			continue
		}
		for _, r := range regs {
			if isLogosServer(r.Command) && r.Vault != "" {
				return r.Vault
			}
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
