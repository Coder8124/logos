package setup

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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
	cmd := hostCommand(cli, "plugin", "--help")
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

// UninstallPluginSteps take the plugin back out. The way in is automated and
// the way out was a printed instruction, which is homework handed to someone at
// the worst possible moment — and a plugin left behind keeps starting its own
// MCP server, running its session-start hooks and updating itself daily after a
// run that reported success.
func UninstallPluginSteps() [][]string {
	return [][]string{{"plugin", "uninstall", PluginRef}}
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
		if err := runPluginStep(cli, step); err != nil {
			return err
		}
	}
	return nil
}

// pluginStepTimeout bounds one step. A plugin install reaches the network, and
// a hung one would hang setup itself with no way out but ^C. A variable so a
// test of the timeout does not take two minutes to run.
var pluginStepTimeout = 2 * time.Minute

// runPluginStep runs one step under that timeout.
//
// The timeout is the context's rather than a race between this function and a
// goroutine writing the command's output: on the timeout branch that goroutine
// was still running while this one had already returned, and both touched the
// same `out` and `err` — a data race, which is to say garbage in the very
// message that says what went wrong.
func runPluginStep(cli string, step []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), pluginStepTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, cli, step...)
	cmd.Env = hostCLIEnv()
	inOwnProcessGroup(cmd)
	cmd.Cancel = func() error { return killTree(cmd) }
	// The pipes this reads are inherited by whatever claude started, so a child
	// that ignored the kill must not be able to hold the read open after it.
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return fmt.Errorf("`%s` did not finish in %s", PluginCommand(step), pluginStepTimeout)
	}
	if err != nil {
		return fmt.Errorf("`%s`: %v: %s", PluginCommand(step), err, strings.TrimSpace(string(out)))
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
	// `mcp get logos`, not `mcp list`: list health-checks every server Claude
	// Code has, including the plugin installed seconds earlier, and one failed
	// start of a plugin's server turns Logos off for fifteen minutes. This is
	// the last thing setup does on the plugin path, so it was poisoning the
	// install it had just reported as finished. `mcp get` exits non-zero when
	// there is no such server, which is the common case and nothing to remove.
	if err := hostCommand(cli, "mcp", "get", Name).Run(); err != nil {
		return nil
	}
	_, err := viaCLI(cli, []string{"mcp", "remove", "--scope", "user", Name})
	return err
}

// pluginFailureWindow is how long Claude Code skips a plugin's MCP server for
// after one failed start, and pluginServerName is what it calls ours.
const (
	pluginFailureWindow = 15 * time.Minute
	pluginServerName    = "plugin:logos:logos"
)

// PluginConnectionSkipped reports how much of that window is left. Claude Code
// records a plugin server's failed start in its own cache and then skips that
// server in every session started for the next fifteen minutes, saying so in a
// line most people never see. Without this, the machine simply has no Logos and
// doctor's other checks all pass: the plugin is installed, enabled, current.
func PluginConnectionSkipped(now time.Time) (time.Duration, bool) {
	path := inHome(".claude", "mcp-needs-auth-cache.json")
	if path == "" {
		return 0, false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	// Claude Code's file, so only what is needed is read from it, and a shape
	// it changes later reads as "nothing cached" rather than as an error.
	var cache map[string]struct {
		Timestamp int64 `json:"timestamp"`
	}
	if json.Unmarshal(raw, &cache) != nil {
		return 0, false
	}
	entry, ok := cache[pluginServerName]
	if !ok {
		return 0, false
	}
	left := pluginFailureWindow - now.Sub(time.UnixMilli(entry.Timestamp))
	if left <= 0 {
		return 0, false
	}
	return left, true
}
