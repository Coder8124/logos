package setup

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// Hosts returns every MCP host brain knows how to connect to, in the order
// they are reported.
//
// Each entry is a promise to keep working as somebody else's application
// changes, so a host is added when people ask for it, not speculatively. Aider
// is not here because it has no MCP client to register with; `brain setup
// --print-config` is the answer for a client that is not on the list.
func Hosts() []Host {
	return []Host{
		claudeCode(), claudeDesktop(), cursor(), codex(),
		cline(), clineCLI(), devin(), copilotCLI(), copilotVSCode(),
	}
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
			outcome, err := viaCLI("claude", args)
			if err != nil || outcome != Updated {
				return outcome, err
			}
			// "already exists" is Claude Code refusing to replace the entry,
			// not a sign that it matches. Taken as success, re-running setup
			// with a new vault or a new binary left Claude Code on the old one
			// while reporting it connected. Replace it.
			if _, err := viaCLI("claude", []string{"mcp", "remove", "--scope", "user", Name}); err != nil {
				return Failed, fmt.Errorf("could not replace the existing brain entry in Claude Code: %w", err)
			}
			if outcome, err = viaCLI("claude", args); err != nil {
				return Failed, fmt.Errorf("removed the old brain entry from Claude Code but could not add the new one — run `brain setup` again: %w", err)
			}
			if outcome != Registered {
				return Failed, fmt.Errorf("Claude Code still refuses to replace its brain entry after removing it — check `claude mcp list`")
			}
			return Updated, nil
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

// jsonHost is a host whose registration is a merge into one JSON file:
// entry shapes brain's server the way that host reads it, under root.
func jsonHost(name, path, root string, detect func() bool, entry func(Server) any) Host {
	return Host{
		Name:   name,
		Detect: func() bool { return path != "" && detect() },
		Where:  func() string { return path },
		Config: func() string { return path },
		Register: func(s Server) (Outcome, error) {
			return mergeServers(path, root, entry(s))
		},
		List: func() ([]Registration, error) { return readServerBlock(path, root) },
	}
}

func plainEntry(s Server) any { return serverEntry{Command: s.Bin, Args: s.Args, Env: s.Env} }

// cline is the Cline VS Code extension. It keeps its servers in the
// extension's global storage, not in VS Code's own settings, and the storage
// directory appears once the extension has run.
func cline() Host {
	ext := ""
	if dir := vscodeUserDir(); dir != "" {
		ext = joinPath(dir, "globalStorage", "saoudrizwan.claude-dev")
	}
	path := ""
	if ext != "" {
		path = joinPath(ext, "settings", "cline_mcp_settings.json")
	}
	return jsonHost("Cline", path, "mcpServers", func() bool { return exists(ext) }, plainEntry)
}

// clineCLI is Cline's terminal client. Its docs name ~/.cline/mcp.json, but
// the CLI never reads that file; this is the one its code loads.
func clineCLI() Host {
	path := inHome(".cline", "data", "settings", "cline_mcp_settings.json")
	return jsonHost("Cline CLI", path, "mcpServers", func() bool {
		return onPath("cline") || exists(inHome(".cline"))
	}, plainEntry)
}

// devin is Devin for Terminal. `devin mcp add` defaults to a scope bound to
// the directory it ran in, so the user-scope file is written directly: a
// memory that exists in one folder is not a connected brain.
func devin() Host {
	path := inHome(".config", "devin", "mcp_config.json")
	if runtime.GOOS == "windows" {
		path = ""
		if dir := appData(); dir != "" {
			path = joinPath(dir, "devin", "mcp_config.json")
		}
	}
	return jsonHost("Devin", path, "mcpServers", func() bool {
		return onPath("devin") || exists(parent(path))
	}, plainEntry)
}

// copilotCLI is GitHub Copilot's terminal client. Written as a file rather
// than through `copilot mcp add`, like the other hosts added with it: the file
// is the documented contract, and a CLI invocation no test may run is a
// registration nobody has verified. It skips a server with no
// type and exposes none of its tools without a tools list. ~/.copilot alone
// is not evidence of it: the VS Code extension leaves ~/.copilot/ide behind.
func copilotCLI() Host {
	path := inHome(".copilot", "mcp-config.json")
	return jsonHost("Copilot CLI", path, "mcpServers", func() bool {
		return onPath("copilot") || exists(path)
	}, func(s Server) any {
		return struct {
			Type string `json:"type"`
			serverEntry
			Tools []string `json:"tools"`
		}{"local", serverEntry{Command: s.Bin, Args: s.Args, Env: s.Env}, []string{"*"}}
	})
}

// copilotVSCode is GitHub Copilot's agent mode in VS Code, which reads the
// user-level mcp.json. VS Code names the map "servers", not mcpServers, and
// types each entry.
func copilotVSCode() Host {
	user := vscodeUserDir()
	path := ""
	if user != "" {
		path = joinPath(user, "mcp.json")
	}
	return jsonHost("Copilot in VS Code", path, "servers", func() bool { return exists(user) },
		func(s Server) any {
			return struct {
				Type string `json:"type"`
				serverEntry
			}{"stdio", serverEntry{Command: s.Bin, Args: s.Args, Env: s.Env}}
		})
}

// vscodeUserDir is VS Code's per-user settings directory, per platform.
func vscodeUserDir() string {
	switch runtime.GOOS {
	case "darwin":
		return inHome("Library", "Application Support", "Code", "User")
	case "windows":
		if dir := appData(); dir != "" {
			return joinPath(dir, "Code", "User")
		}
		return ""
	default:
		return inHome(".config", "Code", "User")
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
