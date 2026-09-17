package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Coder8124/logos/internal/setup"
)

// pinnedHosts stands in for the machine's real host configs, with claude-code
// wired to dir.
func pinnedHosts(dir string) []setup.Host {
	return []setup.Host{{
		Name:   "claude-code",
		Detect: func() bool { return true },
		Where:  func() string { return "" },
		List: func() ([]setup.Registration, error) {
			return []setup.Registration{{Name: "logos", Command: "logos mcp serve", Vault: dir}}, nil
		},
	}}
}

// #95: the plugin's hooks run with a bare environment, so `logos resume` inside
// a session fell through to the machine pointer while the MCP server in that
// same session used the vault its host config pins. The session restored from
// one disk and checkpointed to the other, and the user reported that their
// checkpoints had stopped arriving. A hook says which host it is running in;
// that host's pin is the more specific answer and wins over the pointer.
func TestAHookInAHostUsesTheVaultThatHostPinsRatherThanTheMachinePointer(t *testing.T) {
	pinned := t.TempDir()
	t.Setenv("LOGOS_HOST", "claude-code")
	t.Setenv("LOGOS_VAULT", "")
	os.Unsetenv("LOGOS_VAULT")

	adoptHostPin(pinnedHosts(pinned))

	if got := os.Getenv("LOGOS_VAULT"); got != pinned {
		t.Errorf("the hook resolved to %q, not the vault its host is wired to (%q)", got, pinned)
	}
}

// An explicit LOGOS_VAULT is somebody naming a vault for this one process — a
// scratch vault, a doctor run against a copy. Overruling it with a config file
// would make the variable a suggestion.
func TestAnExplicitVaultIsNotOverruledByTheHostPin(t *testing.T) {
	chosen := t.TempDir()
	t.Setenv("LOGOS_HOST", "claude-code")
	t.Setenv("LOGOS_VAULT", chosen)

	adoptHostPin(pinnedHosts(t.TempDir()))

	if got := os.Getenv("LOGOS_VAULT"); got != chosen {
		t.Errorf("an explicitly chosen vault was replaced by the host pin: %q", got)
	}
}

// A pin naming a vault that is not there is how this bug would come back
// wearing the other face: a stale config would move every hook onto a directory
// logos then creates and reports as a healthy zero (#96). The pointer is the
// safer answer, so a pin that does not resolve is left alone.
func TestAPinNamingAVaultThatIsNotThereIsIgnored(t *testing.T) {
	t.Setenv("LOGOS_HOST", "claude-code")
	t.Setenv("LOGOS_VAULT", "")
	os.Unsetenv("LOGOS_VAULT")

	adoptHostPin(pinnedHosts(filepath.Join(t.TempDir(), "deleted-last-week")))

	if got := os.Getenv("LOGOS_VAULT"); got != "" {
		t.Errorf("a pin pointing at nothing was adopted: %q", got)
	}
}

// Outside a host there is no pin to inherit, and reading every config on the
// machine to answer a plain `logos resume` would be both slow and wrong.
func TestOutsideAHostNothingIsAdopted(t *testing.T) {
	t.Setenv("LOGOS_HOST", "")
	os.Unsetenv("LOGOS_HOST")
	t.Setenv("LOGOS_VAULT", "")
	os.Unsetenv("LOGOS_VAULT")

	adoptHostPin(pinnedHosts(t.TempDir()))

	if got := os.Getenv("LOGOS_VAULT"); got != "" {
		t.Errorf("a plain CLI run adopted a host's pin: %q", got)
	}
}
