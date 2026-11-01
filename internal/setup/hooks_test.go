package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no file at %s: %v", path, err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("%s is not JSON: %v\n%s", path, err, raw)
	}
	return out
}

func hookCommands(t *testing.T, path string) []string {
	t.Helper()
	hooks, _ := readJSON(t, path)["hooks"].(map[string]any)
	events, _ := hooks["sessionStart"].([]any)
	var out []string
	for _, e := range events {
		m, _ := e.(map[string]any)
		s, _ := m["command"].(string)
		out = append(out, s)
	}
	return out
}

// Registering the MCP server gives a model tools it may or may not call. What
// makes continuity happen without the user asking for it is a session-start
// hook, and Cursor has one — so a Cursor user was getting the weaker half of
// Logos while the report said "connected" exactly as it did for Claude Code.
func TestCursorGetsASessionStartHookAndNotOnlyTheServer(t *testing.T) {
	home := fakeHome(t, ".cursor")

	r := Install(server(), []Host{hostNamed(t, "Cursor")})

	if r[0].Outcome == Failed {
		t.Fatalf("Cursor was not wired: %v", r[0].Err)
	}
	if r[0].HookErr != nil {
		t.Fatalf("hook: %v", r[0].HookErr)
	}
	cmds := hookCommands(t, filepath.Join(home, ".cursor", "hooks.json"))
	if len(cmds) != 1 || !strings.Contains(cmds[0], "hook cursor session-start") {
		t.Errorf("Cursor has no logos session-start hook: %q", cmds)
	}
	// Invariant 3: a feature that installed itself silently reads as one that
	// did not install at all.
	if r[0].Hooked != Registered {
		t.Errorf("the hook was installed but not reported: %q", r[0].Hooked)
	}
	if r[0].Tier != TierHooks {
		t.Errorf("tier = %q, want %q", r[0].Tier, TierHooks)
	}
}

// A hooks file is the user's own. Overwriting it would take out whatever else
// they run at session start, and that is not a loss setup gets to cause.
func TestInstallingTheHookKeepsWhatTheUserAlreadyRunsAtSessionStart(t *testing.T) {
	home := fakeHome(t, ".cursor")
	path := filepath.Join(home, ".cursor", "hooks.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"hooks":{"sessionStart":[{"command":"theirs.sh"}],"stop":[{"command":"bye.sh"}]},"theirSetting":true}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := installHook(path, "cursor", []string{"/usr/local/bin/logos"}); err != nil {
		t.Fatal(err)
	}

	cmds := hookCommands(t, path)
	if len(cmds) != 2 || cmds[0] != "theirs.sh" {
		t.Errorf("the user's own session-start hook is gone: %q", cmds)
	}
	file := readJSON(t, path)
	if file["theirSetting"] != true {
		t.Errorf("the rest of the file was dropped: %v", file)
	}
	hooks, _ := file["hooks"].(map[string]any)
	if _, ok := hooks["stop"]; !ok {
		t.Errorf("their other events were dropped: %v", hooks)
	}
}

// Running setup again — after a `brew upgrade`, or with a new --vault — used to
// be the safe thing to do. An appended hook would leave two, the older one
// pointing at a binary that may no longer be there.
func TestRunningSetupTwiceLeavesOneHookPointingAtThisLogos(t *testing.T) {
	home := fakeHome(t, ".cursor")
	path := filepath.Join(home, ".cursor", "hooks.json")

	if _, err := installHook(path, "cursor", []string{"/old/bin/logos"}); err != nil {
		t.Fatal(err)
	}
	outcome, err := installHook(path, "cursor", []string{"/new/bin/logos"})
	if err != nil {
		t.Fatal(err)
	}

	cmds := hookCommands(t, path)
	if len(cmds) != 1 {
		t.Fatalf("setup added a second hook: %q", cmds)
	}
	if !strings.HasPrefix(cmds[0], "/new/bin/logos ") {
		t.Errorf("the hook still runs the old binary: %q", cmds[0])
	}
	if outcome != Updated {
		t.Errorf("outcome = %q, want %q", outcome, Updated)
	}
}

// A path with a space in it is a path the host's shell splits in two, and the
// hook then runs a command that does not exist — silently, at every session.
func TestAHookCommandQuotesABinaryPathWithASpaceInIt(t *testing.T) {
	home := fakeHome(t, ".cursor")
	path := filepath.Join(home, ".cursor", "hooks.json")

	if _, err := installHook(path, "cursor", []string{"/Users/a b/bin/logos"}); err != nil {
		t.Fatal(err)
	}

	if cmds := hookCommands(t, path); !strings.HasPrefix(cmds[0], `'/Users/a b/bin/logos' `) {
		t.Errorf("the path was left for the shell to split: %q", cmds[0])
	}
}

// A malformed hooks file is refused, not replaced: the same promise mergeJSON
// makes about an MCP config.
func TestAHooksFileThatIsNotJSONIsLeftAlone(t *testing.T) {
	home := fakeHome(t, ".cursor")
	path := filepath.Join(home, ".cursor", "hooks.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := installHook(path, "cursor", []string{"/usr/local/bin/logos"}); err == nil {
		t.Fatal("a file logos cannot read was rewritten anyway")
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != "{not json" {
		t.Errorf("the file was changed: %q", raw)
	}
}

// A host with no session-start hook is not broken, it is thinner, and the
// report has to say which it is rather than calling both "connected".
func TestAHostWithNoHookIsReportedAsTheThinnerIntegration(t *testing.T) {
	if got := TierOf(hostNamed(t, "Devin")); got != TierMCP {
		t.Errorf("Devin tier = %q, want %q", got, TierMCP)
	}
	if got := TierOf(hostNamed(t, "Claude Code")); got != TierPlugin {
		t.Errorf("Claude Code tier = %q, want %q", got, TierPlugin)
	}
}

// A hook left behind after uninstall runs a logos that is no longer there, and
// the host reports that failure at the user on every single session.
func TestUninstallTakesTheSessionStartHookOutToo(t *testing.T) {
	home := fakeHome(t, ".cursor")
	path := filepath.Join(home, ".cursor", "hooks.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"hooks":{"sessionStart":[{"command":"theirs.sh"}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installHook(path, "cursor", []string{"/usr/local/bin/logos"}); err != nil {
		t.Fatal(err)
	}

	r := Uninstall([]Host{hostNamed(t, "Cursor")})

	if len(r) != 1 || !r[0].Unhooked {
		t.Fatalf("the hook was left running a logos that is gone: %+v", r)
	}
	cmds := hookCommands(t, path)
	if len(cmds) != 1 || cmds[0] != "theirs.sh" {
		t.Errorf("uninstall did not leave the user's own hooks alone: %q", cmds)
	}
}

// entryFields returns the session-start entries whole, not just their commands,
// which is the difference this test turns on.
func entryFields(t *testing.T, path string) []map[string]any {
	t.Helper()
	hooks, _ := readJSON(t, path)["hooks"].(map[string]any)
	events, _ := hooks["sessionStart"].([]any)
	var out []map[string]any
	for _, e := range events {
		m, _ := e.(map[string]any)
		out = append(out, m)
	}
	return out
}

// Keeping the user's other hook is not the same as keeping it intact. The file
// was decoded into a struct with one field, so a timeout, a name, a matcher —
// anything the host understands and logos does not — was dropped on the way
// back out, by both install and uninstall. Logos's own entry is the only one it
// may rewrite.
func TestInstallingTheHookKeepsEveryFieldOfTheUsersOwnEntry(t *testing.T) {
	home := fakeHome(t, ".cursor")
	path := filepath.Join(home, ".cursor", "hooks.json")
	theirs := `{"command":"~/bin/theirs.sh","timeout":30,"name":"audit","matcher":"*"}`
	if err := os.WriteFile(path, []byte(`{"version":1,"hooks":{"sessionStart":[`+theirs+`]}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := installHook(path, "cursor", []string{"/usr/local/bin/logos"}); err != nil {
		t.Fatal(err)
	}
	assertTheirEntryIsWhole(t, path)

	if _, err := removeHook(path); err != nil {
		t.Fatal(err)
	}
	assertTheirEntryIsWhole(t, path)
}

func assertTheirEntryIsWhole(t *testing.T, path string) {
	t.Helper()
	entries := entryFields(t, path)
	if len(entries) == 0 {
		t.Fatalf("the user's session-start entry is gone from %s", path)
	}
	e := entries[0]
	for field, want := range map[string]any{"command": "~/bin/theirs.sh", "timeout": 30.0, "name": "audit", "matcher": "*"} {
		if e[field] != want {
			t.Errorf("the user's own hook lost %s: have %v, want %v", field, e[field], want)
		}
	}
}

// An npx install whose copy to ~/.local/bin fails runs logos as `npx -y
// @noeton/logos`, and the hook took only the first word of that: every Cursor
// and Codex session start ran `npx hook cursor session-start`, which is not
// Logos — it is npm being asked over the network for a package called "hook".
func TestAHookRunsTheWholeCommandTheServerRunsNotJustItsFirstWord(t *testing.T) {
	home := fakeHome(t, ".cursor")
	path := filepath.Join(home, ".cursor", "hooks.json")

	if _, err := installHook(path, "cursor", []string{"npx", "-y", "@noeton/logos"}); err != nil {
		t.Fatal(err)
	}

	got := entryFields(t, path)
	if len(got) != 1 {
		t.Fatalf("want one entry, got %+v", got)
	}
	if cmd, _ := got[0]["command"].(string); cmd != "npx -y @noeton/logos hook cursor session-start" {
		t.Errorf("hook command = %q; the package is missing, so the host runs npm's idea of `hook`", cmd)
	}
}

// codexHookCommands is every command under Codex's SessionStart event, which
// nests handlers inside matcher groups the way Claude Code's settings do.
func codexHookCommands(t *testing.T, path string) []string {
	t.Helper()
	hooks, _ := readJSON(t, path)["hooks"].(map[string]any)
	groups, _ := hooks["SessionStart"].([]any)
	var out []string
	for _, g := range groups {
		m, _ := g.(map[string]any)
		handlers, _ := m["hooks"].([]any)
		for _, h := range handlers {
			e, _ := h.(map[string]any)
			if e["type"] != "command" {
				t.Errorf("a Codex handler without type \"command\" is not run: %v", e)
			}
			s, _ := e["command"].(string)
			out = append(out, s)
		}
	}
	return out
}

// The hook was built from the whole MCP launch line, so every Cursor and Codex
// install ran `logos mcp serve hook cursor session-start`: an MCP server that
// read the host's JSON as a request, printed nothing and exited 0. Setup said
// "session-start hook registered" and no session ever opened with a handoff.
func TestTheSessionStartHookRunsTheHookAndNotTheMCPServer(t *testing.T) {
	home := fakeHome(t, ".cursor")
	path := filepath.Join(home, ".cursor", "hooks.json")

	Install(server(), []Host{hostNamed(t, "Cursor")})
	if cmds := hookCommands(t, path); len(cmds) != 1 || cmds[0] != "/usr/local/bin/logos hook cursor session-start" {
		t.Errorf("Cursor's hook = %q, want the binary and `hook cursor session-start` alone", cmds)
	}

	npx := Server{Bin: "npx", Args: []string{"-y", "@noeton/logos", "mcp", "serve"}}
	Install(npx, []Host{hostNamed(t, "Cursor")})
	if cmds := hookCommands(t, path); len(cmds) != 1 || cmds[0] != "npx -y @noeton/logos hook cursor session-start" {
		t.Errorf("an npx install's hook = %q, want npx's launcher without `mcp serve`", cmds)
	}
}

// Codex reads hooks.json in Claude Code's shape — SessionStart, matcher groups,
// typed handlers — not Cursor's flat sessionStart list, so a hook written in
// Cursor's shape was never run however correct its command was.
func TestCodexGetsItsHookInTheShapeCodexReads(t *testing.T) {
	home := fakeHome(t, ".codex")
	path := filepath.Join(home, ".codex", "hooks.json")
	// Install skips a host it cannot find, and CI has no codex: without a stub
	// this passed on a developer's machine and found no hooks file on CI.
	loggingStub(t, filepath.Join(home, "bin", "codex"))
	t.Setenv("PATH", filepath.Join(home, "bin")+string(os.PathListSeparator)+"/usr/bin:/bin")

	r := Install(server(), []Host{hostNamed(t, "Codex")})
	if r[0].HookErr != nil {
		t.Fatalf("hook: %v", r[0].HookErr)
	}

	if cmds := codexHookCommands(t, path); len(cmds) != 1 || cmds[0] != "/usr/local/bin/logos hook codex session-start" {
		t.Errorf("Codex SessionStart = %q, want one logos handler", cmds)
	}
	file := readJSON(t, path)
	hooks, _ := file["hooks"].(map[string]any)
	if _, ok := hooks["sessionStart"]; ok {
		t.Errorf("Codex's file still has Cursor's sessionStart key: %v", hooks)
	}
	if _, ok := file["version"]; ok {
		t.Errorf("a new Codex hooks file got Cursor's version key: %v", file)
	}
}

// Every Codex user who ran setup before this has logos's broken entry in the
// flat shape. Running setup again has to move it, not add a working one beside
// a dead one, and leave the user's own entries where they were.
func TestSetupMovesAnEarlierCodexHookIntoCodexsShape(t *testing.T) {
	home := fakeHome(t, ".codex")
	path := filepath.Join(home, ".codex", "hooks.json")
	old := `{"version":1,"hooks":{"sessionStart":[{"command":"/opt/homebrew/opt/logos-mcp/bin/logos mcp serve hook codex session-start"},{"command":"theirs.sh"}],"SessionStart":[{"matcher":"startup","hooks":[{"type":"command","command":"mine.sh"}]}]}}`
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}

	outcome, err := installHook(path, "codex", []string{"/usr/local/bin/logos"})
	if err != nil {
		t.Fatal(err)
	}
	if outcome != Updated {
		t.Errorf("outcome = %q, want %q: replacing a hook that never ran is news", outcome, Updated)
	}
	if cmds := hookCommands(t, path); len(cmds) != 1 || cmds[0] != "theirs.sh" {
		t.Errorf("flat sessionStart = %q, want only the user's own entry", cmds)
	}
	cmds := codexHookCommands(t, path)
	if len(cmds) != 2 || cmds[0] != "mine.sh" || cmds[1] != "/usr/local/bin/logos hook codex session-start" {
		t.Errorf("SessionStart = %q, want the user's handler then logos's", cmds)
	}
}

// Uninstall has to find logos in the shape setup now writes, or it leaves a
// hook that runs a binary the user just removed.
func TestUninstallTakesLogosOutOfCodexsSessionStart(t *testing.T) {
	home := fakeHome(t, ".codex")
	path := filepath.Join(home, ".codex", "hooks.json")
	if err := os.WriteFile(path, []byte(`{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"mine.sh"}]}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installHook(path, "codex", []string{"/usr/local/bin/logos"}); err != nil {
		t.Fatal(err)
	}

	removed, err := removeHook(path)
	if err != nil || !removed {
		t.Fatalf("removeHook = %v, %v; want true", removed, err)
	}
	if cmds := codexHookCommands(t, path); len(cmds) != 1 || cmds[0] != "mine.sh" {
		t.Errorf("SessionStart after uninstall = %q, want only the user's handler", cmds)
	}
}

// JSON's null unmarshals into a map as nil, so a hooks file holding
// `"hooks": null` panicked setup with "assignment to entry in nil map" after
// the server was already registered: a stack trace, a half-wired host, and
// no later host wired at all (#356).
func TestAHooksFileWithANullHooksBlockGetsTheHook(t *testing.T) {
	home := fakeHome(t, ".cursor")
	hooks := filepath.Join(home, ".cursor", "hooks.json")
	if err := os.WriteFile(hooks, []byte(`{"version": 1, "hooks": null}`), 0o600); err != nil {
		t.Fatal(err)
	}

	r := Install(server(), []Host{hostNamed(t, "Cursor")})

	if r[0].Outcome == Failed || r[0].HookErr != nil {
		t.Fatalf("result = %+v", r[0])
	}
	if cmds := hookCommands(t, hooks); len(cmds) != 1 {
		t.Errorf("hooks = %q, want the logos hook", cmds)
	}
}

// The same nil map in an MCP config: `"mcpServers": null` panicked setup
// before it wrote anything (#355).
func TestAConfigWithANullServersBlockGetsLogos(t *testing.T) {
	home := fakeHome(t, ".cursor")
	path := filepath.Join(home, ".cursor", "mcp.json")
	if err := os.WriteFile(path, []byte(`{"mcpServers": null, "theme": "keep-me"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	r := Install(server(), []Host{hostNamed(t, "Cursor")})

	if r[0].Outcome == Failed {
		t.Fatalf("Cursor was not wired: %v", r[0].Err)
	}
	cfg := readJSON(t, path)
	if servers, _ := cfg["mcpServers"].(map[string]any); servers[Name] == nil {
		t.Errorf("no logos entry: %v", cfg)
	}
	if cfg["theme"] != "keep-me" {
		t.Errorf("the user's other key was lost: %v", cfg)
	}
}
