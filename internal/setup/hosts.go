package setup

import (
	"encoding/json"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// Hosts returns every MCP host brain knows how to connect to, in the order
// they are reported.
//
// The list is deliberately short. Each entry is a promise to keep working as
// somebody else's application changes, and a host added speculatively is a
// promise nobody asked for.
func Hosts() []Host {
	return []Host{claudeCode(), claudeDesktop(), cursor(), codex()}
}

// claudeCode registers through `claude mcp add`.
//
// --scope user, not the default local scope: local scope binds the server to
// whatever directory the command happened to run in, and a memory that only
// exists in one folder is not what anyone means by connecting their brain.
func claudeCode() Host {
	return Host{
		Name:   "Claude Code",
		Detect: func() bool { return onPath("claude") },
		Where:  func() string { return "claude mcp add --scope user" },
		// User-scope MCP servers live in ~/.claude.json, alongside a great deal
		// else that is not ours to lose.
		Config: func() string { return inHome(".claude.json") },
		Register: func(s Server) (Outcome, error) {
			args := []string{"mcp", "add", "--scope", "user", Name}
			for k, v := range s.Env {
				args = append(args, "-e", k+"="+v)
			}
			args = append(args, "--", s.Bin)
			args = append(args, s.Args...)
			return viaCLI("claude", args)
		},
		List: func() ([]Registration, error) {
			out, err := exec.Command("claude", "mcp", "list").CombinedOutput()
			if err != nil {
				return nil, err
			}
			return parseClaudeMCPList(out), nil
		},
	}
}

// parseClaudeMCPList reads `claude mcp list`'s human-readable report, not a
// machine format — the same bet Register already makes by matching
// "already exists" in that command's own stdout (see viaCLI). Each server
// prints as "name: command - status"; anything else (a banner line, a blank
// line) has no colon-then-dash shape and is skipped rather than guessed at.
func parseClaudeMCPList(out []byte) []Registration {
	var regs []Registration
	for _, line := range strings.Split(string(out), "\n") {
		name, rest, ok := strings.Cut(line, ": ")
		if !ok {
			continue
		}
		cmd, _, ok := strings.Cut(rest, " - ")
		if !ok {
			continue
		}
		regs = append(regs, Registration{Name: strings.TrimSpace(name), Command: strings.TrimSpace(cmd)})
	}
	return regs
}

// codex registers through `codex mcp add`.
//
// Codex takes environment as a repeated --env flag before the -- separator.
func codex() Host {
	return Host{
		Name:   "Codex",
		Detect: func() bool { return onPath("codex") },
		Where:  func() string { return "codex mcp add" },
		Config: func() string { return inHome(".codex", "config.toml") },
		Register: func(s Server) (Outcome, error) {
			args := []string{"mcp", "add", Name}
			for k, v := range s.Env {
				args = append(args, "--env", k+"="+v)
			}
			args = append(args, "--", s.Bin)
			args = append(args, s.Args...)
			return viaCLI("codex", args)
		},
	}
}

// claudeDesktop has no CLI, so its config file is merged.
func claudeDesktop() Host {
	path := claudeDesktopConfig()
	return Host{
		Name: "Claude Desktop",
		// The directory, not the file: Claude Desktop creates the file only
		// once it has an MCP server, so requiring it would mean never
		// connecting the users who most need this.
		Detect: func() bool { return path != "" && exists(parent(path)) },
		Where:  func() string { return path },
		Config: func() string { return path },
		Register: func(s Server) (Outcome, error) {
			return mergeJSON(path, s)
		},
		List: func() ([]Registration, error) { return readMCPServers(path) },
	}
}

// cursor has no CLI either. ~/.cursor/mcp.json is the global scope; a
// project-local .cursor/mcp.json also exists, and global is the right default
// for a memory that should follow the user across projects.
func cursor() Host {
	path := inHome(".cursor", "mcp.json")
	return Host{
		Name:   "Cursor",
		Detect: func() bool { return path != "" && (exists(parent(path)) || onPath("cursor")) },
		Where:  func() string { return path },
		Config: func() string { return path },
		Register: func(s Server) (Outcome, error) {
			return mergeJSON(path, s)
		},
		List: func() ([]Registration, error) { return readMCPServers(path) },
	}
}

// claudeDesktopConfig is where Claude Desktop keeps its MCP servers, per
// platform.
func claudeDesktopConfig() string {
	switch runtime.GOOS {
	case "darwin":
		return inHome("Library", "Application Support", "Claude", "claude_desktop_config.json")
	case "windows":
		if dir := appData(); dir != "" {
			return joinPath(dir, "Claude", "claude_desktop_config.json")
		}
		return ""
	default:
		return inHome(".config", "Claude", "claude_desktop_config.json")
	}
}

func onPath(bin string) bool {
	_, err := exec.LookPath(bin)
	return err == nil
}

// LogosPlugin reports whether the Logos plugin is installed in Claude Code, and
// at which version. The plugin carries its own MCP server, so registering brain
// with `claude mcp add` on top of it lists every tool twice and pays the
// per-session cost twice. Claude Code records installed plugins in this file,
// keyed "<plugin>@<marketplace>".
func LogosPlugin() (version string, installed bool) {
	path := inHome(".claude", "plugins", "installed_plugins.json")
	if path == "" {
		return "", false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var reg struct {
		Plugins map[string][]struct {
			Version string `json:"version"`
		} `json:"plugins"`
	}
	if json.Unmarshal(raw, &reg) != nil {
		return "", false
	}
	for key, installs := range reg.Plugins {
		if strings.HasPrefix(key, "logos@") && len(installs) > 0 {
			return installs[0].Version, true
		}
	}
	return "", false
}
