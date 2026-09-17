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

// hookFile is a host's user-level hooks file, in the shape both Cursor and
// Codex use: a version, and events naming the commands to run.
// It is decoded field by field rather than marshalled back whole, because
// whatever else the user keeps in that file has to survive the round trip.
type hookFile struct {
	Version int
	Hooks   map[string]json.RawMessage
	rest    map[string]json.RawMessage // everything in the file, ours included
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

func logosEntryFor(host, bin string) hookEntry {
	cmd, _ := json.Marshal(quoteForShell(bin) + " hook " + host + " session-start")
	return hookEntry{"command": cmd}
}

// installHook puts `logos hook <host> session-start` in the host's hooks file
// and leaves everything else in it alone.
//
// Merged rather than written: a hooks file is a user's own, and overwriting it
// would take out whatever else they run at session start. A logos entry already
// there is replaced, not duplicated — otherwise re-running setup after moving
// the binary leaves two entries, one of them pointing at a logos that is gone.
func installHook(path, host, bin string) (Outcome, error) {
	if path == "" {
		return Failed, fmt.Errorf("logos does not know where %s keeps its hooks", host)
	}
	f, err := loadHookFile(path)
	if err != nil {
		return Failed, err
	}
	var entries []hookEntry
	if raw, ok := f.Hooks["sessionStart"]; ok && len(raw) > 0 {
		if err := json.Unmarshal(raw, &entries); err != nil {
			return Failed, fmt.Errorf(
				"%s has a sessionStart block logos cannot read, so it was left alone; fix or move it and re-run: %w",
				path, err)
		}
	}
	want := logosEntryFor(host, bin)
	had := false
	for i, e := range entries {
		if strings.Contains(e.command(), " hook "+host+" session-start") {
			had, entries[i] = true, want
			break
		}
	}
	if !had {
		entries = append(entries, want)
	}
	encoded, err := json.Marshal(entries)
	if err != nil {
		return Failed, err
	}
	f.Hooks["sessionStart"] = encoded
	if err := saveHookFile(path, f); err != nil {
		return Failed, err
	}
	if had {
		return Updated, nil
	}
	return Registered, nil
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
	version, err := json.Marshal(f.Version)
	if err != nil {
		return err
	}
	if f.rest == nil {
		f.rest = map[string]json.RawMessage{}
	}
	f.rest["version"], f.rest["hooks"] = version, hooks
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
	raw, ok := f.Hooks["sessionStart"]
	if !ok || len(raw) == 0 {
		return false, nil
	}
	var entries []hookEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return false, nil // not a shape logos wrote, so not logos's to change
	}
	kept, found := entries[:0], false
	for _, e := range entries {
		if strings.Contains(e.command(), " hook cursor session-start") ||
			strings.Contains(e.command(), " hook codex session-start") {
			found = true
			continue
		}
		kept = append(kept, e)
	}
	if !found {
		return false, nil
	}
	encoded, err := json.Marshal(kept)
	if err != nil {
		return false, err
	}
	f.Hooks["sessionStart"] = encoded
	if err := saveHookFile(path, f); err != nil {
		return false, err
	}
	return true, nil
}
