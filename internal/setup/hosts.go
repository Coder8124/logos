package setup

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// Hosts returns every MCP host logos knows how to connect to, in the order
// they are reported.
//
// Each entry is a promise to keep working as somebody else's application
// changes, so a host is added when people ask for it, not speculatively. Aider
// is not here because it has no MCP client to register with; `logos setup
// --print-config` is the answer for a client that is not on the list.
func Hosts() []Host {
	return []Host{
		claudeCode(), claudeDesktop(), cursor(), codex(),
		cline("Cline", "Code"), cline("Cline in Cursor", "Cursor"), cline("Cline in Windsurf", "Windsurf"), clineCLI(), devin(), copilotCLI(), copilotVSCode(),
		opencode(), amp(), grokBuild(),
	}
}

// claudeCode registers through `claude mcp add`.
//
// --scope user, not the default local scope: local scope binds the server to
// whatever directory the command happened to run in, and a memory that only
// exists in one folder is not what anyone means by connecting their logos.
func claudeCode() Host {
	return Host{
		Name:   "Claude Code",
		Detect: func() bool { return claudeCLI() != "" },
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
			outcome, err := viaCLI(claudeCLI(), args)
			if err != nil || outcome != Updated {
				return outcome, err
			}
			// "already exists" is Claude Code refusing to replace the entry,
			// not a sign that it matches. Taken as success, re-running setup
			// with a new vault or a new binary left Claude Code on the old one
			// while reporting it connected. Replace it.
			if _, err := viaCLI(claudeCLI(), []string{"mcp", "remove", "--scope", "user", Name}); err != nil {
				return Failed, fmt.Errorf("could not replace the existing logos entry in Claude Code: %w", err)
			}
			if outcome, err = viaCLI(claudeCLI(), args); err != nil {
				return Failed, fmt.Errorf("removed the old logos entry from Claude Code but could not add the new one — run `logos setup` again: %w", err)
			}
			if outcome != Registered {
				return Failed, fmt.Errorf("Claude Code still refuses to replace its logos entry after removing it — check `claude mcp list`")
			}
			return Updated, nil
		},
		List: func() ([]Registration, error) {
			out, err := hostCommand(claudeCLI(), "mcp", "list").CombinedOutput()
			if err != nil {
				return nil, err
			}
			return parseClaudeMCPList(out), nil
		},
		Remove: func(name string) (bool, error) {
			out, err := hostCommand(claudeCLI(), "mcp", "list").CombinedOutput()
			if err != nil {
				return false, err
			}
			for _, r := range parseClaudeMCPList(out) {
				if r.Name == name && isLogosServer(r.Command) {
					if _, err := viaCLI(claudeCLI(), []string{"mcp", "remove", "--scope", "user", name}); err != nil {
						return false, err
					}
					return true, nil
				}
			}
			return false, nil
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
		Detect: func() bool { return codexCLI() != "" },
		Where:  func() string { return "codex mcp add" },
		Config: func() string { return inHome(".codex", "config.toml") },
		// Read off the file, not `codex mcp list`: the file is where the
		// environment is written down, and asking a host's own CLI at doctor
		// time starts a process per host. Without this Codex was the one host
		// whose registrations nothing could see, so a Codex wired to a deleted
		// binary or an npx cache path passed every check (#90).
		List: func() ([]Registration, error) { return readCodexServers(inHome(".codex", "config.toml")) },
		Register: func(s Server) (Outcome, error) {
			args := []string{"mcp", "add", Name}
			for k, v := range s.Env {
				args = append(args, "--env", k+"="+v)
			}
			args = append(args, "--", s.Bin)
			args = append(args, s.Args...)
			return viaCLI(codexCLI(), args)
		},
		Hooks: func(s Server) (Outcome, error) {
			p := codexHooksPath()
			if _, err := backupHooks(p); err != nil {
				return Failed, err
			}
			return installHook(p, "codex", append([]string{s.Bin}, s.Args...))
		},
		Remove: func(name string) (bool, error) {
			raw, err := os.ReadFile(inHome(".codex", "config.toml"))
			if err != nil || !codexHasEntry(string(raw), name) {
				return false, nil
			}
			if _, err := viaCLI(codexCLI(), []string{"mcp", "remove", name}); err != nil {
				return false, err
			}
			return true, nil
		},
	}
}

// codexHasEntry looks for the [mcp_servers.<name>] table and checks it runs
// mcp serve. Read as lines rather than parsed: this is the only TOML logos
// reads, and a table is all it needs to find.
//
// Comments are stripped first, for the same reason the value parser strips
// them: `[mcp_servers.logos] # logos` is that table, and a commented-out
// `# args = ["mcp", "serve"]` is not its contents.
func codexHasEntry(toml, name string) bool {
	in, body := false, ""
	for _, line := range strings.Split(toml, "\n") {
		t := strings.TrimSpace(stripComment(line))
		if strings.HasPrefix(t, "[") {
			in = t == "[mcp_servers."+name+"]"
			continue
		}
		if in {
			body += t + " "
		}
	}
	return strings.Contains(body, `"mcp"`) && strings.Contains(body, `"serve"`)
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
		List:   func() ([]Registration, error) { return readMCPServers(path) },
		Remove: func(name string) (bool, error) { return removeJSON(path, "mcpServers", name) },
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
		Hooks: func(s Server) (Outcome, error) {
			p := cursorHooksPath()
			if _, err := backupHooks(p); err != nil {
				return Failed, err
			}
			return installHook(p, "cursor", append([]string{s.Bin}, s.Args...))
		},
		Remove: func(name string) (bool, error) { return removeJSON(path, "mcpServers", name) },
	}
}

// jsonHost is a host whose registration is a merge into one JSON file:
// entry shapes logos's server the way that host reads it, under root.
func jsonHost(name, path, root string, detect func() bool, entry func(Server) any) Host {
	return Host{
		Name:   name,
		Detect: func() bool { return path != "" && detect() },
		Where:  func() string { return path },
		Config: func() string { return path },
		Register: func(s Server) (Outcome, error) {
			return mergeServers(path, root, entry(s))
		},
		List:   func() ([]Registration, error) { return readServerBlock(path, root) },
		Remove: func(name string) (bool, error) { return removeJSON(path, root, name) },
	}
}

// removeJSON deletes the entry called name under root when it runs logos.
func removeJSON(path, root, name string) (bool, error) {
	return removeEntry(path, root, name, func() ([]Registration, error) { return readServerBlock(path, root) })
}

// removeEntry is removeJSON for a host whose entries read back through read,
// because not every host keeps a command as one string.
func removeEntry(path, root, name string, read func() ([]Registration, error)) (bool, error) {
	regs, err := read()
	if err != nil {
		return false, err
	}
	for _, r := range regs {
		if r.Name != name || !isLogosServer(r.Command) {
			continue
		}
		cfg, servers, err := loadServers(path, root)
		if err != nil {
			return false, err
		}
		delete(servers, name)
		if err := saveServers(path, root, cfg, servers); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

func plainEntry(s Server) any { return serverEntry{Command: s.Bin, Args: s.Args, Env: s.Env} }

// cline is the Cline extension in a VS Code-family editor. It keeps its
// servers in the extension's global storage, not in the editor's own settings,
// and the storage directory appears once the extension has run. Cursor and
// Windsurf each have their own storage, so Cline in them was never seen when
// only VS Code's was looked at.
func cline(name, app string) Host {
	ext := ""
	if dir := editorUserDir(app); dir != "" {
		ext = joinPath(dir, "globalStorage", "saoudrizwan.claude-dev")
	}
	path := ""
	if ext != "" {
		path = joinPath(ext, "settings", "cline_mcp_settings.json")
	}
	return jsonHost(name, path, "mcpServers", func() bool { return exists(ext) }, plainEntry)
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
// memory that exists in one folder is not a connected logos.
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
	// COPILOT_HOME moves the whole config directory; writing ~/.copilot anyway
	// reported a registration into a file Copilot never reads.
	if dir := os.Getenv("COPILOT_HOME"); dir != "" {
		path = joinPath(dir, "mcp-config.json")
	}
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
	// VS Code alone is not Copilot. The Copilot Chat extension is what reads
	// this file, and an mcp.json already there means VS Code's MCP is in use.
	detect := func() bool {
		if exists(path) {
			return true
		}
		found, _ := filepath.Glob(inHome(".vscode", "extensions", "github.copilot-chat-*"))
		return len(found) > 0
	}
	return jsonHost("Copilot in VS Code", path, "servers", detect,
		func(s Server) any {
			return struct {
				Type string `json:"type"`
				serverEntry
			}{"stdio", serverEntry{Command: s.Bin, Args: s.Args, Env: s.Env}}
		})
}

// opencode keeps its servers under "mcp", each typed, with the command and its
// arguments in one array and the environment under "environment". An entry in
// the mcpServers shape is not an error to opencode; it is simply never started.
//
// The global config is read from the XDG config directory on every platform.
// opencode.jsonc is written only when it is the one there: a second file
// beside it would be a config the user never opens.
func opencode() Host {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		dir = inHome(".config")
	}
	if dir != "" {
		dir = joinPath(dir, "opencode")
	}
	path := ""
	if dir != "" {
		path = joinPath(dir, "opencode.json")
		if jsonc := joinPath(dir, "opencode.jsonc"); !exists(path) && exists(jsonc) {
			path = jsonc
		}
	}
	read := func() ([]Registration, error) { return readOpencodeServers(path) }
	return Host{
		Name: "opencode",
		// Its installer puts the binary in ~/.opencode/bin, which a shell that
		// started logos may not have on PATH.
		Detect: func() bool {
			return path != "" && (onPath("opencode") || exists(dir) || exists(inHome(".opencode")))
		},
		Where:  func() string { return path },
		Config: func() string { return path },
		Register: func(s Server) (Outcome, error) {
			return mergeServers(path, "mcp", opencodeEntry{
				Type: "local", Command: append([]string{s.Bin}, s.Args...), Enabled: true, Environment: s.Env,
			})
		},
		List:   read,
		Remove: func(name string) (bool, error) { return removeEntry(path, "mcp", name, read) },
	}
}

type opencodeEntry struct {
	Type        string            `json:"type"`
	Command     []string          `json:"command,omitempty"`
	Enabled     bool              `json:"enabled"`
	Environment map[string]string `json:"environment,omitempty"`
}

// readOpencodeServers is readServerBlock for opencode's shape. A remote server
// has a url and no command, and reads back with an empty one.
func readOpencodeServers(path string) ([]Registration, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil, nil
	}
	cfg := mcpConfig{}
	std, _ := standardJSON(raw)
	if err := json.Unmarshal(std, &cfg); err != nil {
		return nil, err
	}
	servers := map[string]opencodeEntry{}
	if block, ok := cfg["mcp"]; ok && len(block) > 0 {
		if err := json.Unmarshal(block, &servers); err != nil {
			return nil, err
		}
	}
	// enabled is read apart because opencode starts an entry that leaves it
	// out; as a plain bool, absent would read as switched off.
	enabled := map[string]struct {
		Enabled *bool `json:"enabled"`
	}{}
	_ = json.Unmarshal(cfg["mcp"], &enabled)
	out := make([]Registration, 0, len(servers))
	for name, s := range servers {
		r := Registration{Name: name, Command: strings.Join(s.Command, " "), Vault: s.Environment["LOGOS_VAULT"]}
		if e := enabled[name].Enabled; e != nil && !*e {
			r.Disabled = true
		}
		if len(s.Command) > 0 {
			r.Server = Server{Bin: s.Command[0], Args: s.Command[1:], Env: s.Environment}
		}
		out = append(out, r)
	}
	return out, nil
}

// amp is Sourcegraph's Amp. Its user settings file holds the whole of Amp's
// configuration, with the servers under the literal key "amp.mcpServers" — a
// dotted name, not a nested object. Amp documents no Windows location, so it is
// not guessed at there; --print-config covers it.
func amp() Host {
	path := inHome(".config", "amp", "settings.json")
	if runtime.GOOS == "windows" {
		path = ""
	}
	return jsonHost("Amp", path, "amp.mcpServers", func() bool {
		return onPath("amp") || exists(parent(path))
	}, plainEntry)
}

// grokBuild is xAI's Grok Build CLI, whose user config is TOML with servers in
// the [mcp_servers.<name>] tables Codex uses. GROK_HOME moves its whole home,
// sessions and config alike. Written as a file rather than through `grok mcp
// add`, for the reason copilotCLI gives.
//
// It also reads Claude Code's ~/.claude.json, beneath its own config, so a
// machine with Claude Code wired already had a logos in Grok. Its own entry is
// still written: the one in config.toml is the one it prefers, and the only one
// that survives someone turning the Claude compatibility off.
func grokBuild() Host {
	dir := os.Getenv("GROK_HOME")
	if dir == "" {
		dir = inHome(".grok")
	}
	path := ""
	if dir != "" {
		path = joinPath(dir, "config.toml")
	}
	return Host{
		Name: "Grok Build",
		// Neither ~/.grok nor a grok command is evidence: the unrelated
		// community grok-cli keeps its settings there and installs a grok of its
		// own, and taking it for Grok Build writes a config.toml nothing reads —
		// reported as registered, and detected on every run after. Grok Build's
		// config or its sessions are. A Grok Build not yet run once is skipped,
		// and says so, rather than guessed at.
		Detect: func() bool {
			return path != "" && (exists(path) || exists(joinPath(dir, "sessions")))
		},
		Where:    func() string { return path },
		Config:   func() string { return path },
		Register: func(s Server) (Outcome, error) { return mergeTOMLServer(path, s) },
		List:     func() ([]Registration, error) { return readCodexServers(path) },
		Remove:   func(name string) (bool, error) { return removeTOMLServer(path, name) },
	}
}

// vscodeUserDir is VS Code's per-user settings directory, per platform.
func vscodeUserDir() string { return editorUserDir("Code") }

// editorUserDir is the per-user settings directory of a VS Code-family editor
// whose application directory is app, per platform.
func editorUserDir(app string) string {
	switch runtime.GOOS {
	case "darwin":
		return inHome("Library", "Application Support", app, "User")
	case "windows":
		if dir := appData(); dir != "" {
			return joinPath(dir, app, "User")
		}
		return ""
	default:
		return inHome(".config", app, "User")
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

// noBundledCLIsEnv turns off finding a host CLI outside PATH. testenv.Run sets
// it (spelled out there too, since testenv cannot import setup): taking the
// CLIs off PATH is what keeps tests from running the developer's real claude
// (see testenv), and a bundled copy would walk straight past that.
const noBundledCLIsEnv = "LOGOS_TEST_NO_BUNDLED_CLIS"

// codexApp is where the Codex desktop app keeps its CLI. A variable so tests
// can point it at a stub.
var codexApp = "/Applications/Codex.app/Contents/Resources/codex"

// codexCLI is the codex setup runs: the one on PATH, else the copy the desktop
// app ships inside its bundle. Someone who only uses the app has no codex on
// PATH, and setup called Codex "not installed" and wired nothing.
func codexCLI() string {
	if path, err := exec.LookPath("codex"); err == nil {
		return path
	}
	if os.Getenv(noBundledCLIsEnv) == "" && isExecutable(codexApp) {
		return codexApp
	}
	return ""
}

// claudeCLI is the claude setup runs: the one on PATH, else the copy bundled
// in the newest Claude Code extension for VS Code, for someone who uses Claude
// Code only inside the editor. Where the binary sits inside the extension is
// the extension's business, so the folder is searched rather than assumed; a
// folder with no executable claude in it drives a separately installed CLI and
// is not an install on its own.
func claudeCLI() string {
	if path, err := exec.LookPath("claude"); err == nil {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil || os.Getenv(noBundledCLIsEnv) != "" {
		return ""
	}
	exts, _ := filepath.Glob(filepath.Join(home, ".vscode*", "extensions", "anthropic.claude-code-*"))
	// Newest first by modification time: version strings do not sort as
	// text (2.1.9 after 2.1.270), and VS Code leaves old versions behind.
	mtime := func(p string) int64 {
		if fi, err := os.Stat(p); err == nil {
			return fi.ModTime().UnixNano()
		}
		return 0
	}
	sort.SliceStable(exts, func(i, j int) bool { return mtime(exts[i]) > mtime(exts[j]) })
	for _, ext := range exts {
		found := ""
		filepath.WalkDir(ext, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() && d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			if !d.IsDir() && d.Name() == "claude" && isExecutable(p) {
				found = p
				return filepath.SkipAll
			}
			return nil
		})
		if found != "" {
			return found
		}
	}
	return ""
}

func isExecutable(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular() && fi.Mode().Perm()&0o111 != 0
}

func onPath(bin string) bool {
	_, err := exec.LookPath(bin)
	return err == nil
}

// PluginRecord is what Claude Code's own files say about the Logos plugin.
type PluginRecord struct {
	Version   string
	Installed bool // some logos@ install is recorded
	Connects  bool // it gives Claude Code Logos in every project
	Why       string
}

// LogosPlugin reports whether the Logos plugin connects Claude Code, and at
// which version. The plugin carries its own MCP server, so registering logos
// with `claude mcp add` on top of it lists every tool twice and pays the
// per-session cost twice.
func LogosPlugin() (version string, connects bool) {
	r := LogosPluginRecord()
	return r.Version, r.Connects
}

// LogosPluginRecord reads the plugin's install record. Claude Code records
// installed plugins in installed_plugins.json, keyed "<plugin>@<marketplace>",
// and the on/off switch separately in settings.json's enabledPlugins.
//
// Any logos@ key used to count as connected, so a plugin turned off in
// /plugin, or installed for a single project, made setup skip Claude Code and
// leave it with no Logos anywhere else. Only an install at user (or managed)
// scope that the user settings do not disable counts. A disable in one
// project's settings cannot be seen from here, which Why says.
func LogosPluginRecord() PluginRecord {
	path := inHome(".claude", "plugins", "installed_plugins.json")
	if path == "" {
		return PluginRecord{}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return PluginRecord{}
	}
	var reg struct {
		Plugins map[string][]struct {
			Scope       string `json:"scope"`
			ProjectPath string `json:"projectPath"`
			Version     string `json:"version"`
		} `json:"plugins"`
	}
	if json.Unmarshal(raw, &reg) != nil {
		return PluginRecord{}
	}
	var enabled struct {
		EnabledPlugins map[string]bool `json:"enabledPlugins"`
	}
	if settings, err := os.ReadFile(inHome(".claude", "settings.json")); err == nil {
		_ = json.Unmarshal(settings, &enabled)
	}
	// Map order is random, so a disabled logos@ from one marketplace must not
	// hide an enabled one from another.
	var found PluginRecord
	for key, installs := range reg.Plugins {
		if !strings.HasPrefix(key, "logos@") || len(installs) == 0 {
			continue
		}
		r := PluginRecord{Version: installs[0].Version, Installed: true}
		if on, set := enabled.EnabledPlugins[key]; set && !on {
			r.Why = "installed but disabled in Claude Code"
			found = r
			continue
		}
		for _, in := range installs {
			if in.Scope == "" || in.Scope == "user" || in.Scope == "managed" {
				r.Version, r.Connects = in.Version, true
				return r
			}
		}
		r.Why = "installed only for " + installs[0].ProjectPath
		if installs[0].ProjectPath == "" {
			r.Why = "installed at " + installs[0].Scope + " scope only"
		}
		found = r
	}
	return found
}
