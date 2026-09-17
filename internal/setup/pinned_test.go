package setup_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Coder8124/logos/internal/setup"
)

// pinnedHost writes the config file a host would have written for this
// registration, because that file is where the pin really lives — a host's
// listing command does not report environment.
func pinnedHost(t *testing.T, name, command, vault string) setup.Host {
	t.Helper()
	cfg := filepath.Join(t.TempDir(), "config.json")
	entry := fmt.Sprintf(`{"mcpServers":{"logos":{"command":%q,"args":["mcp","serve"],"env":{"LOGOS_VAULT":%q}}}}`, command, vault)
	if err := os.WriteFile(cfg, []byte(entry), 0o600); err != nil {
		t.Fatal(err)
	}
	return setup.Host{
		Name:   name,
		Detect: func() bool { return true },
		Where:  func() string { return "/tmp/" + name + ".json" },
		Config: func() string { return cfg },
	}
}

// #95: a host config pins the MCP server to one vault with LOGOS_VAULT, but the
// plugin's hooks run with a bare environment and fall through to the machine
// pointer. When the two disagree, one session restores from one disk and
// checkpoints to another, each side internally consistent and the user's work
// apparently vanishing. The hooks can only inherit the pin if something can
// read it back.
func TestThePinnedVaultIsReadBackFromTheHostThatWiredIt(t *testing.T) {
	hosts := []setup.Host{
		pinnedHost(t, "cursor", "logos", "/Users/bob/other"),
		pinnedHost(t, "claude-code", "/opt/homebrew/bin/logos", "/Users/bob/brain"),
	}
	if got := setup.PinnedVault(hosts, "claude-code"); got != "/Users/bob/brain" {
		t.Errorf("the vault claude-code pins was not read back: %q", got)
	}
}

// Another MCP server's entry pins its own directories, and taking one for the
// vault would point logos at somebody else's data.
func TestAnEntryThatIsNotLogosIsNotReadAsAVaultPin(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "claude.json")
	entry := `{"mcpServers":{"filesystem":{"command":"npx","args":["-y","@modelcontextprotocol/server-filesystem"],"env":{"LOGOS_VAULT":"/Users/bob/notes"}}}}`
	if err := os.WriteFile(cfg, []byte(entry), 0o600); err != nil {
		t.Fatal(err)
	}
	hosts := []setup.Host{{
		Name:   "claude-code",
		Detect: func() bool { return true },
		Where:  func() string { return "" },
		Config: func() string { return cfg },
	}}
	if got := setup.PinnedVault(hosts, "claude-code"); got != "" {
		t.Errorf("a non-logos server's environment was read as the vault pin: %q", got)
	}
}

// No pin is the normal case — `claude mcp list` does not show environment at
// all — and it must read as "nothing to say", never as an empty vault path that
// a caller could act on.
func TestAHostThatCannotReportItsEnvironmentPinsNothing(t *testing.T) {
	broken := filepath.Join(t.TempDir(), "claude.json")
	if err := os.WriteFile(broken, []byte("{not json at all"), 0o600); err != nil {
		t.Fatal(err)
	}
	unreadable := setup.Host{
		Name:   "claude-code",
		Detect: func() bool { return true },
		Where:  func() string { return "" },
		Config: func() string { return broken },
	}
	if got := setup.PinnedVault([]setup.Host{unreadable}, "claude-code"); got != "" {
		t.Errorf("an unreadable host reported a pin: %q", got)
	}
	if got := setup.PinnedVault(nil, "claude-code"); got != "" {
		t.Errorf("an unknown host reported a pin: %q", got)
	}
}

// Claude Code is the host the plugin's hooks actually run inside, and the one
// whose listing cannot answer this: `claude mcp list` prints name, command and
// status, never environment. Reading the pin back off the config file it wrote
// is the only way the hooks get the vault their own session's server uses —
// without it #95 stays open on the host it matters most on.
func TestAHostWhoseListingHidesTheEnvironmentIsReadFromItsConfigFile(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "claude.json")
	entry := `{"mcpServers":{"logos":{"command":"/opt/homebrew/bin/logos","args":["mcp","serve"],"env":{"LOGOS_VAULT":"/Users/bob/brain"}}}}`
	if err := os.WriteFile(cfg, []byte(entry), 0o600); err != nil {
		t.Fatal(err)
	}
	hosts := []setup.Host{{
		Name:   "Claude Code",
		Detect: func() bool { return true },
		Where:  func() string { return "claude mcp add" },
		Config: func() string { return cfg },
		// What `claude mcp list` really returns: no environment at all.
		List: func() ([]setup.Registration, error) {
			return []setup.Registration{{Name: "logos", Command: "/opt/homebrew/bin/logos mcp serve"}}, nil
		},
	}}
	if got := setup.PinnedVault(hosts, "Claude Code"); got != "/Users/bob/brain" {
		t.Errorf("the vault in Claude Code's own config was not read back: %q", got)
	}
}

// The hooks name their host in a shell variable, where "Claude Code" with a
// space and a capital is the awkward spelling. The name is a label for one
// thing, so the two spellings have to mean it.
func TestTheHostNameIsMatchedHoweverItIsSpelled(t *testing.T) {
	hosts := []setup.Host{pinnedHost(t, "Claude Code", "logos", "/Users/bob/brain")}
	if got := setup.PinnedVault(hosts, "claude-code"); got != "/Users/bob/brain" {
		t.Errorf("the hook's spelling of its host did not match the host: %q", got)
	}
}

// #90's other half: the split doctor was built to catch is invisible on the
// one host most people use, because `claude mcp list` prints no environment.
// A Claude Code wired to a vault the machine has since moved off must show up
// as a host on another vault, not as a host with nothing to report.
func TestAHostLeftOnAnotherVaultIsSeenEvenWhenItsListingHidesTheEnvironment(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "claude.json")
	entry := `{"mcpServers":{"logos":{"command":"/opt/homebrew/bin/logos","args":["mcp","serve"],"env":{"LOGOS_VAULT":"/Users/bob/old-vault"}}}}`
	if err := os.WriteFile(cfg, []byte(entry), 0o600); err != nil {
		t.Fatal(err)
	}
	hosts := []setup.Host{{
		Name:   "Claude Code",
		Detect: func() bool { return true },
		Where:  func() string { return "claude mcp add" },
		Config: func() string { return cfg },
		List: func() ([]setup.Registration, error) {
			return []setup.Registration{{Name: "logos", Command: "/opt/homebrew/bin/logos mcp serve"}}, nil
		},
	}}
	names, vaults := setup.OnOtherVault(hosts, "/Users/bob/brain")
	if len(names) != 1 || names[0] != "Claude Code" || vaults[0] != "/Users/bob/old-vault" {
		t.Errorf("Claude Code left on an old vault went unreported: %v %v", names, vaults)
	}
}

// The pin is read off the config file and nothing else. PinnedVault used to ask
// the host's own listing first, which for Claude Code means running `claude mcp
// list` — a Node process, started before argument dispatch, on every single
// `logos` invocation including the per-tool-call hooks. Worse, `claude mcp list`
// connects to the servers it lists, so logos launched by the plugin would start
// a claude that starts a logos, with the LOGOS_VAULT that guards re-entry
// stripped from the child on the way down. The listing could never answer the
// question anyway: `claude mcp list` prints no environment.
func TestThePinIsReadWithoutRunningTheHostsOwnCommand(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "claude.json")
	entry := `{"mcpServers":{"logos":{"command":"/opt/homebrew/bin/logos","args":["mcp","serve"],"env":{"LOGOS_VAULT":"/Users/bob/brain"}}}}`
	if err := os.WriteFile(cfg, []byte(entry), 0o600); err != nil {
		t.Fatal(err)
	}
	asked := false
	hosts := []setup.Host{{
		Name:   "Claude Code",
		Detect: func() bool { return true },
		Where:  func() string { return "claude mcp add" },
		Config: func() string { return cfg },
		List: func() ([]setup.Registration, error) {
			asked = true
			return []setup.Registration{{Name: "logos", Command: "logos mcp serve", Vault: "/Users/bob/from-the-listing"}}, nil
		},
	}}
	if got := setup.PinnedVault(hosts, "claude-code"); got != "/Users/bob/brain" {
		t.Errorf("the pin came from %q, not the host's config file", got)
	}
	if asked {
		t.Error("PinnedVault ran the host's own listing command to answer a question the config file already answers")
	}
}
