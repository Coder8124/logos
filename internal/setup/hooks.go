package setup

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Tier names how much of Logos a host actually gets. Registering the MCP
// server is the floor: it gives a model tools it can choose to call. What makes
// continuity happen without anyone remembering to ask for it is a session-start
// hook, and only some hosts have one — Claude Code through the plugin, Cursor
// and Codex through a user-level hooks file. The tier is printed per host
// because "connected" meant three different things and setup said the same word
// for all of them.
type Tier string

const (
	TierPlugin Tier = "plugin — restores on its own"
	TierHooks  Tier = "MCP + session hooks"
	TierMCP    Tier = "MCP only — resume by asking"
)

// TierOf reports the richest integration this host supports.
func TierOf(h Host) Tier {
	switch {
	case h.Name == "Claude Code":
		return TierPlugin
	case h.Hooks != nil:
		return TierHooks
	default:
		return TierMCP
	}
}

// hookFile is a host's user-level hooks file: an object of events naming the
// commands to run. Cursor's also carries a version; Codex's does not, and
// writing one into a file Codex owns is a key it never asked for.
// It is decoded field by field rather than marshalled back whole, because
// whatever else the user keeps in that file has to survive the round trip.
type hookFile struct {
	Version   int
	versioned bool // write Version back: the file had one, or it is Cursor's
	Hooks     map[string]json.RawMessage
	rest      map[string]json.RawMessage // everything in the file, ours included
}

// A session-start entry is kept as the object the file held, not as a struct of
// the one field logos reads. Hosts put more on an entry than a command — a
// timeout, a name, a matcher — and a struct with a single field silently
// dropped all of it on the round trip, so installing (or uninstalling) logos
// rewrote hooks it never wrote.
type hookEntry map[string]json.RawMessage

// command is the entry's command, or "" when it has none or holds something
// other than a string. Only logos's own entry is ever matched on it.
func (e hookEntry) command() string {
	var s string
	if raw, ok := e["command"]; ok {
		json.Unmarshal(raw, &s)
	}
	return s
}

// hookArgv is how the hook runs logos: the server's launch line without its
// `mcp serve`. Built from the whole launch line, every hook was
// `logos mcp serve hook cursor session-start` — an MCP server that read the
// host's JSON as a request, printed nothing and exited 0, so setup reported a
// hook and no session ever opened with a handoff.
func hookArgv(s Server) []string {
	argv := append([]string{s.Bin}, s.Args...)
	if n := len(argv); n >= 2 && argv[n-2] == "mcp" && argv[n-1] == "serve" {
		argv = argv[:n-2]
	}
	return argv
}

// isLogosHook reports whether a command is logos's session-start hook for
// host, under whatever binary or launcher an earlier setup wrote it with.
func isLogosHook(cmd, host string) bool {
	return strings.Contains(cmd, " hook "+host+" session-start")
}

// logosEntryFor builds the entry from the whole command line the server is
// launched with, not just its first word. An npx install whose copy to
// ~/.local/bin failed falls back to running logos through npx, and taking only
// the binary wrote the hook as `npx hook cursor session-start` — which asks npm
// for a package called "hook", at every session start, over the network.
func logosEntryFor(host string, argv []string) hookEntry {
	quoted := make([]string, 0, len(argv))
	for _, a := range argv {
		quoted = append(quoted, quoteForShell(a))
	}
	cmd, _ := json.Marshal(strings.Join(quoted, " ") + " hook " + host + " session-start")
	return hookEntry{"command": cmd}
}

// installHook puts `logos hook <host> session-start` in the host's hooks file
// and leaves everything else in it alone.
//
// Merged rather than written: a hooks file is a user's own, and overwriting it
// would take out whatever else they run at session start. A logos entry already
// there is replaced, not duplicated — otherwise re-running setup after moving
// the binary leaves two entries, one of them pointing at a logos that is gone.
func installHook(path, host string, argv []string) (Outcome, error) {
	if path == "" {
		return Failed, fmt.Errorf("logos does not know where %s keeps its hooks", host)
	}
	f, err := loadHookFile(path)
	if err != nil {
		return Failed, err
	}
	want := logosEntryFor(host, argv)
	var had bool
	if host == "codex" {
		had, err = installCodexHook(path, &f, want)
	} else {
		f.versioned = true
		had, err = installFlatHook(path, &f, host, want)
	}
	if err != nil {
		return Failed, err
	}
	if err := saveHookFile(path, f); err != nil {
		return Failed, err
	}
	if had {
		return Updated, nil
	}
	return Registered, nil
}

// installFlatHook is Cursor's shape: sessionStart, a list of entries.
func installFlatHook(path string, f *hookFile, host string, want hookEntry) (bool, error) {
	entries, err := flatEntries(path, f)
	if err != nil {
		return false, err
	}
	had := false
	for i, e := range entries {
		if isLogosHook(e.command(), host) {
			had, entries[i] = true, want
			break
		}
	}
	if !had {
		entries = append(entries, want)
	}
	return had, setFlatEntries(f, entries)
}

// installCodexHook is Codex's shape, which is Claude Code's: SessionStart, a
// list of matcher groups, each holding typed handlers. Earlier setups wrote
// Cursor's shape here, which Codex never read; that entry is moved rather than
// left beside the working one, and counts as an update, because the user had
// a hook that setup reported and that never ran.
func installCodexHook(path string, f *hookFile, want hookEntry) (bool, error) {
	want["type"] = json.RawMessage(`"command"`)
	// Codex never reads the flat block, so one logos cannot parse is no reason
	// to leave Codex without a hook; it is left exactly as it was.
	had, _ := dropFlatLogosHooks(path, f)
	groups, err := codexGroups(path, f)
	if err != nil {
		return false, err
	}
	placed := false
	for _, g := range groups {
		handlers, err := groupHandlers(path, g)
		if err != nil {
			return false, err
		}
		for i, h := range handlers {
			if isLogosHook(h.command(), "codex") {
				handlers[i], placed = want, true
				break
			}
		}
		if placed {
			if g["hooks"], err = json.Marshal(handlers); err != nil {
				return false, err
			}
			break
		}
	}
	if !placed {
		// No matcher: Codex runs the group at every session source — startup,
		// resume, clear and compact — and each of them starts without context.
		hooks, _ := json.Marshal([]hookEntry{want})
		groups = append(groups, hookEntry{"hooks": hooks})
	}
	return had || placed, setCodexGroups(f, groups)
}

func flatEntries(path string, f *hookFile) ([]hookEntry, error) {
	var entries []hookEntry
	if raw, ok := f.Hooks["sessionStart"]; ok && len(raw) > 0 {
		if err := json.Unmarshal(raw, &entries); err != nil {
			return nil, fmt.Errorf(
				"%s has a sessionStart block logos cannot read, so it was left alone; fix or move it and re-run: %w",
				path, err)
		}
	}
	return entries, nil
}

// setFlatEntries writes sessionStart back, dropping the key once nothing is
// left in it rather than leaving an empty event the user never had.
func setFlatEntries(f *hookFile, entries []hookEntry) error {
	if len(entries) == 0 {
		delete(f.Hooks, "sessionStart")
		return nil
	}
	encoded, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	f.Hooks["sessionStart"] = encoded
	return nil
}

// dropFlatLogosHooks takes any logos entry out of the flat sessionStart list
// and reports whether there was one; the user's own entries stay.
func dropFlatLogosHooks(path string, f *hookFile) (bool, error) {
	entries, err := flatEntries(path, f)
	if err != nil || len(entries) == 0 {
		return false, err
	}
	kept, found := entries[:0], false
	for _, e := range entries {
		if isLogosHook(e.command(), "cursor") || isLogosHook(e.command(), "codex") {
			found = true
			continue
		}
		kept = append(kept, e)
	}
	if !found {
		return false, nil
	}
	return true, setFlatEntries(f, kept)
}

func codexGroups(path string, f *hookFile) ([]hookEntry, error) {
	var groups []hookEntry
	if raw, ok := f.Hooks["SessionStart"]; ok && len(raw) > 0 {
		if err := json.Unmarshal(raw, &groups); err != nil {
			return nil, fmt.Errorf(
				"%s has a SessionStart block logos cannot read, so it was left alone; fix or move it and re-run: %w",
				path, err)
		}
	}
	return groups, nil
}

func groupHandlers(path string, g hookEntry) ([]hookEntry, error) {
	var handlers []hookEntry
	if raw, ok := g["hooks"]; ok && len(raw) > 0 {
		if err := json.Unmarshal(raw, &handlers); err != nil {
			return nil, fmt.Errorf(
				"%s has a SessionStart group logos cannot read, so it was left alone; fix or move it and re-run: %w",
				path, err)
		}
	}
	return handlers, nil
}

func setCodexGroups(f *hookFile, groups []hookEntry) error {
	if len(groups) == 0 {
		delete(f.Hooks, "SessionStart")
		return nil
	}
	encoded, err := json.Marshal(groups)
	if err != nil {
		return err
	}
	f.Hooks["SessionStart"] = encoded
	return nil
}

// quoteForShell is enough quoting for a path that goes into a command line a
// host will run through a shell. Homebrew and npm paths have no spaces, but a
// binary the user pinned under "Application Support" does, and an unquoted one
// there made the hook run a command that did not exist.
func quoteForShell(bin string) string {
	if !strings.ContainsAny(bin, " \t'\"$`\\") {
		return bin
	}
	return "'" + strings.ReplaceAll(bin, "'", `'\''`) + "'"
}

func loadHookFile(path string) (hookFile, error) {
	f := hookFile{Version: 1, Hooks: map[string]json.RawMessage{}, rest: map[string]json.RawMessage{}}
	raw, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
		return f, nil
	case err != nil:
		return f, err
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return f, nil
	}
	std, _ := standardJSON(raw)
	if err := json.Unmarshal(std, &f.rest); err != nil {
		return f, fmt.Errorf("%s is not valid JSON, so it was left alone; fix or move it and re-run: %w", path, err)
	}
	if v, ok := f.rest["version"]; ok {
		f.versioned = true
		json.Unmarshal(v, &f.Version)
	}
	if h, ok := f.rest["hooks"]; ok && len(h) > 0 {
		if err := json.Unmarshal(h, &f.Hooks); err != nil {
			return f, fmt.Errorf("%s has a hooks block that is not an object: %w", path, err)
		}
	}
	return f, nil
}

func saveHookFile(path string, f hookFile) error {
	hooks, err := json.Marshal(f.Hooks)
	if err != nil {
		return err
	}
	if f.rest == nil {
		f.rest = map[string]json.RawMessage{}
	}
	f.rest["hooks"] = hooks
	if f.versioned {
		version, err := json.Marshal(f.Version)
		if err != nil {
			return err
		}
		f.rest["version"] = version
	}
	out, err := json.MarshalIndent(f.rest, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return writeHostFile(path, append(out, '\n'))
}

// backupHooks copies a hooks file aside before it is merged, for the same
// reason backupConfig does it for an MCP config: it is the only way back.
func backupHooks(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("could not read %s to back it up, so it was left alone: %w", path, err)
	}
	if err := os.WriteFile(path+".logos-backup", raw, 0o600); err != nil {
		return "", fmt.Errorf("could not back up %s, so it was left alone: %w", path, err)
	}
	return path + ".logos-backup", nil
}

// The two hosts with a user-level hooks file.
func cursorHooksPath() string { return inHome(".cursor", "hooks.json") }
func codexHooksPath() string  { return inHome(".codex", "hooks.json") }

// hooksPathFor is the same lookup by host name, for uninstall, which has the
// host but not the closure that knows its path.
func hooksPathFor(name string) string {
	switch name {
	case "Cursor":
		return cursorHooksPath()
	case "Codex":
		return codexHooksPath()
	}
	return ""
}

// removeHook takes logos's session-start hook out and leaves the rest of the
// file as it was, reporting whether there was one. A hook left behind after
// uninstall runs a binary that is gone, and the host says so at every session.
func removeHook(path string) (bool, error) {
	if path == "" {
		return false, nil
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return false, nil
	}
	f, err := loadHookFile(path)
	if err != nil {
		return false, err
	}
	// A block in a shape logos did not write is not logos's to change, so an
	// unreadable one is skipped here rather than failing the uninstall.
	flat, _ := dropFlatLogosHooks(path, &f)
	nested := dropCodexLogosHooks(path, &f)
	if !flat && !nested {
		return false, nil
	}
	if err := saveHookFile(path, f); err != nil {
		return false, err
	}
	return true, nil
}

// dropCodexLogosHooks takes logos's handler out of SessionStart, and with it
// any group it leaves empty, reporting whether there was one.
func dropCodexLogosHooks(path string, f *hookFile) bool {
	groups, err := codexGroups(path, f)
	if err != nil {
		return false
	}
	kept, found := groups[:0], false
	for _, g := range groups {
		handlers, err := groupHandlers(path, g)
		if err != nil {
			kept = append(kept, g)
			continue
		}
		left := handlers[:0]
		for _, h := range handlers {
			if isLogosHook(h.command(), "codex") {
				found = true
				continue
			}
			left = append(left, h)
		}
		if len(left) == len(handlers) {
			kept = append(kept, g)
			continue
		}
		if len(left) > 0 {
			g["hooks"], _ = json.Marshal(left)
			kept = append(kept, g)
		}
	}
	if found {
		setCodexGroups(f, kept)
	}
	return found
}
