package setup

import (
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// PluginSource is the marketplace `claude plugin marketplace add` takes, and
// PluginRef names the plugin inside it.
const (
	PluginSource = "Coder8124/logos"
	PluginRef    = "logos@logos"
)

// SupportsPluginCommands says whether the Claude Code on this machine can
// install plugins from the command line. `claude plugin` arrived in 2.1; on an
// older one setup still has to fall back to `claude mcp add` and printed
// instructions.
func SupportsPluginCommands() bool {
	cli := claudeCLI()
	if cli == "" {
		return false
	}
	cmd := exec.Command(cli, "plugin", "--help")
	out, err := cmd.CombinedOutput()
	// An older claude exits non-zero on an unknown subcommand, but a `plugin`
	// that is only a stub would pass on the exit code alone, so the commands
	// setup is about to run have to be named in the help itself.
	return err == nil && strings.Contains(string(out), "marketplace")
}

// InstallPluginSteps are the commands that install the Logos plugin: the
// marketplace has to be known before the plugin in it can be installed.
func InstallPluginSteps() [][]string {
	return [][]string{
		{"plugin", "marketplace", "add", PluginSource},
		{"plugin", "install", PluginRef, "--scope", "user"},
	}
}

// UpdatePluginSteps refresh a plugin older than this binary. Claude Code does
// not refresh a third-party marketplace on its own, so updating the plugin
// without updating the marketplace first finds nothing newer.
func UpdatePluginSteps() [][]string {
	return [][]string{
		{"plugin", "marketplace", "update", "logos"},
		{"plugin", "update", PluginRef},
	}
}

// PluginCommand renders one step the way a user would type it, for a dry run.
func PluginCommand(step []string) string {
	return "claude " + strings.Join(step, " ")
}

// RunPluginSteps runs the steps in order and stops at the first failure,
// naming the command that failed and what it printed: a half-done install
// reported as success is how someone ends up with a marketplace and no plugin.
func RunPluginSteps(steps [][]string) error {
	cli := claudeCLI()
	if cli == "" {
		return fmt.Errorf("claude is not installed here")
	}
	for _, step := range steps {
		// A plugin install reaches the network, and a hung one would hang
		// setup itself with no way out but ^C.
		cmd := exec.Command(cli, step...)
		done := make(chan struct{})
		var out []byte
		var err error
		go func() {
			out, err = cmd.CombinedOutput()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Minute):
			cmd.Process.Kill()
			return fmt.Errorf("`%s` did not finish in two minutes", PluginCommand(step))
		}
		if err != nil {
			return fmt.Errorf("`%s`: %v: %s", PluginCommand(step), err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// RemoveServerEntry removes a user-scope MCP entry an earlier setup wrote in
// Claude Code. The plugin carries the same server, so the entry left behind
// would load logos twice.
func RemoveServerEntry() error {
	cli := claudeCLI()
	if cli == "" {
		return nil
	}
	out, err := exec.Command(cli, "mcp", "list").CombinedOutput()
	if err != nil {
		return nil // nothing listable means nothing of ours to remove
	}
	for _, r := range parseClaudeMCPList(out) {
		if r.Name != Name {
			continue
		}
		if _, err := viaCLI(cli, []string{"mcp", "remove", "--scope", "user", Name}); err != nil {
			return err
		}
		return nil
	}
	return nil
}
