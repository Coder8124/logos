package setup

import (
	"os/exec"
	"runtime"
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
		Register: func(s Server) (Outcome, error) {
			args := []string{"mcp", "add", "--scope", "user", Name}
			for k, v := range s.Env {
				args = append(args, "-e", k+"="+v)
			}
			args = append(args, "--", s.Bin)
			args = append(args, s.Args...)
			return viaCLI("claude", args)
		},
	}
}

// codex registers through `codex mcp add`.
//
// Codex takes environment as a repeated --env flag before the -- separator.
func codex() Host {
	return Host{
		Name:   "Codex",
		Detect: func() bool { return onPath("codex") },
		Where:  func() string { return "codex mcp add" },
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
		Register: func(s Server) (Outcome, error) {
			return mergeJSON(path, s)
		},
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
		Register: func(s Server) (Outcome, error) {
			return mergeJSON(path, s)
		},
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
