package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Registering through a host's own CLI rewrites that host's config file just as
// surely as merging it ourselves does — `codex mcp add logos` replaces an
// existing logos entry outright, environment block and all, and says only
// "Added global MCP server 'logos'". Backing up only the hosts logos writes by
// hand left the CLI hosts with no way back from a wrong --vault.
func TestEveryHostWhoseConfigWeKnowIsBackedUpBeforeRegistering(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("the user's own settings\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	hosts := []Host{{
		Name:   "cli-host",
		Detect: func() bool { return true },
		Where:  func() string { return "fake mcp add" },
		Config: func() string { return path },
		Register: func(Server) (Outcome, error) {
			// What the host's CLI does: replaces the file wholesale.
			return Registered, os.WriteFile(path, []byte("clobbered\n"), 0o600)
		},
	}}

	r := Install(server(), hosts)
	if r[0].Outcome != Registered {
		t.Fatalf("outcome = %q (%v)", r[0].Outcome, r[0].Err)
	}
	// A backup nobody is told about is a backup nobody uses (invariant 3), so
	// the result carries the path setup prints.
	if r[0].Backup != path+".logos-backup" {
		t.Errorf("result does not name the backup: %q", r[0].Backup)
	}

	backup, err := os.ReadFile(path + ".logos-backup")
	if err != nil {
		t.Fatalf("no backup was written before the host's CLI rewrote its config: %v", err)
	}
	if string(backup) != "the user's own settings\n" {
		t.Errorf("backup holds %q, not what the file said before", backup)
	}
}

// The rule mergeJSON already followed, now applied to every host: if the backup
// cannot be written the config is left alone. A registration nobody can undo is
// worse than one that did not happen.
func TestAHostIsLeftAloneWhenItsBackupCannotBeWritten(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("original\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil { // readable, not writable
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })

	hosts := []Host{{
		Name:     "cli-host",
		Detect:   func() bool { return true },
		Where:    func() string { return "fake mcp add" },
		Config:   func() string { return path },
		Register: func(Server) (Outcome, error) { t.Error("registered without a backup"); return Registered, nil },
	}}

	r := Install(server(), hosts)
	if r[0].Outcome != Failed {
		t.Fatalf("outcome = %q, want failed", r[0].Outcome)
	}
	if r[0].Err == nil || !strings.Contains(r[0].Err.Error(), "left alone") {
		t.Errorf("error does not say the config was left alone: %v", r[0].Err)
	}
	if b, _ := os.ReadFile(path); string(b) != "original\n" {
		t.Errorf("the config was changed anyway: %q", b)
	}
}

// A host with nothing to back up — no config file yet — still registers. The
// first user of Claude Desktop has no config until an MCP server gives them
// one, and refusing them would be refusing exactly the people this is for.
func TestAHostWithNoConfigYetStillRegisters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-created-yet.json")
	hosts := []Host{{
		Name:     "fresh",
		Detect:   func() bool { return true },
		Where:    func() string { return path },
		Config:   func() string { return path },
		Register: func(Server) (Outcome, error) { return Registered, nil },
	}}

	r := Install(server(), hosts)
	if r[0].Outcome != Registered {
		t.Fatalf("outcome = %q (%v)", r[0].Outcome, r[0].Err)
	}
	// Nothing existed to copy, so there is no backup path to report either.
	if r[0].Backup != "" {
		t.Errorf("named a backup that was never written: %q", r[0].Backup)
	}
	if _, err := os.Stat(path + ".logos-backup"); err == nil {
		t.Error("backed up a file that did not exist")
	}
}

// Every shipped host must say which file its registration rewrites. A host that
// cannot answer is a host that silently gets no backup, which is the bug this
// pair of tests exists for.
func TestEveryShippedHostNamesTheConfigItRewrites(t *testing.T) {
	for _, h := range Hosts() {
		if h.Config == nil {
			t.Errorf("%s does not say which config it rewrites", h.Name)
			continue
		}
		h.Config()
	}
}

// Running setup twice is the normal thing to do — after moving a vault, after
// an update, or just to check. The second run rewrote each config with bytes
// identical to the ones already there, announced every host as "updated", and
// left a .logos-backup beside each one. Reporting work that did not happen is
// the same fault as swallowing work that did (invariants 3 and 4), and the
// litter it drops is in the user's own config directory.
func TestAHostThatIsAlreadyConnectedIsNotReportedAsChanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	const settled = "{\"mcpServers\":{\"logos\":{}}}\n"
	if err := os.WriteFile(path, []byte(settled), 0o600); err != nil {
		t.Fatal(err)
	}

	hosts := []Host{{
		Name:   "settled",
		Detect: func() bool { return true },
		Where:  func() string { return path },
		Config: func() string { return path },
		// A merge that finds logos already pointed where it should be writes
		// the same bytes back.
		Register: func(Server) (Outcome, error) {
			return Updated, os.WriteFile(path, []byte(settled), 0o600)
		},
	}}

	r := Install(server(), hosts)
	if r[0].Outcome != Unchanged {
		t.Errorf("outcome = %q, want %q", r[0].Outcome, Unchanged)
	}
	if r[0].Backup != "" {
		t.Errorf("named a backup of a file nothing changed: %q", r[0].Backup)
	}
	if _, err := os.Stat(path + ".logos-backup"); err == nil {
		t.Error("left a backup beside a config that was never changed")
	}
}

// The test above settles a stub host, and the real merge was never asked the
// same question. A first setup wrote logos's entry in the order its struct
// declares (command, args, env) and a second one in map order (args, command,
// env), so the bytes differed and the backup stayed; and the hooks file's
// backup was written on every run and never taken back. scripts/first-run.sh
// found three .logos-backup files after a second `setup --yes` changed nothing.
func TestASecondSetupLeavesNoBackupBesideTheConfigsItDidNotChange(t *testing.T) {
	home := fakeHome(t, ".cursor")
	cursor := hostNamed(t, "Cursor")

	if r := Install(server(), []Host{cursor}); r[0].Outcome == Failed || r[0].HookErr != nil {
		t.Fatalf("first setup: %v %v", r[0].Err, r[0].HookErr)
	}
	r := Install(server(), []Host{cursor})
	if r[0].Outcome != Unchanged {
		t.Errorf("outcome = %q (%v), want %q", r[0].Outcome, r[0].Err, Unchanged)
	}
	if r[0].Hooked != Unchanged {
		t.Errorf("hook outcome = %q (%v), want %q", r[0].Hooked, r[0].HookErr, Unchanged)
	}
	for _, f := range []string{"mcp.json", "hooks.json"} {
		if _, err := os.Stat(filepath.Join(home, ".cursor", f+".logos-backup")); err == nil {
			t.Errorf("left a backup beside %s, which the second setup did not change", f)
		}
	}
}

// The backup is worth keeping because it is the user's config from before
// logos touched it. Every run wrote a fresh one, so a second setup — which an
// update or a moved vault makes routine — overwrote it with the first run's
// output; and since a second run changes nothing, it then deleted that as
// litter. Either way the only copy of the user's original was gone. A backup an
// earlier run left is never overwritten or taken back, whether this run
// changed the file or not; what a changing run found goes beside it, so the
// run before the last can still be undone too.
func TestASecondSetupKeepsTheBackupOfTheUsersOriginalConfig(t *testing.T) {
	home := fakeHome(t, ".cursor")
	cursor := hostNamed(t, "Cursor")
	const (
		mcp   = "{\"mcpServers\":{\"theirs\":{\"command\":\"their-server\"}}}\n"
		hooks = "{\"version\":1,\"hooks\":{\"stop\":[{\"command\":\"their-hook\"}]}}\n"
	)
	originals := map[string]string{"mcp.json": mcp, "hooks.json": hooks}
	for f, body := range originals {
		if err := os.WriteFile(filepath.Join(home, ".cursor", f), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	moved := server()
	moved.Env = map[string]string{"LOGOS_VAULT": "/Users/someone/elsewhere"}
	mcpPath := filepath.Join(home, ".cursor", "mcp.json")
	var connected []byte
	for i, s := range []Server{server(), server(), moved} {
		if i == 2 {
			// The user rewrites their hooks without logos's, so this run puts
			// it back and changes that file too.
			edited := "{\"version\":1,\"hooks\":{\"stop\":[{\"command\":\"their-other-hook\"}]}}\n"
			if err := os.WriteFile(filepath.Join(home, ".cursor", "hooks.json"), []byte(edited), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		r := Install(s, []Host{cursor})
		if r[0].Outcome == Failed || r[0].HookErr != nil {
			t.Fatalf("setup %d: %v %v", i+1, r[0].Err, r[0].HookErr)
		}
		if i == 1 {
			connected, _ = os.ReadFile(mcpPath)
		}
		if i == 2 {
			// The move: what it replaced is kept, and both copies are named.
			if got, _ := os.ReadFile(mcpPath + ".logos-backup.previous"); string(got) != string(connected) {
				t.Errorf("the config the move replaced was not kept beside the original: %q", got)
			}
			if r[0].Backup != mcpPath+".logos-backup.previous" || r[0].Original != mcpPath+".logos-backup" {
				t.Errorf("backup = %q, original = %q", r[0].Backup, r[0].Original)
			}
			// Setup has no line naming a hooks file's copies, so it keeps only
			// the original rather than drop a second file nobody is told about.
			if _, err := os.Lstat(filepath.Join(home, ".cursor", "hooks.json.logos-backup.previous")); err == nil {
				t.Error("left an unannounced .previous beside hooks.json")
			}
		}
		for _, f := range []string{"mcp.json", "hooks.json"} {
			if _, err := os.Lstat(filepath.Join(home, ".cursor", f+".logos-backup.previous.pending")); err == nil {
				t.Errorf("after setup %d a .pending copy of %s was left behind", i+1, f)
			}
		}
		for f, body := range originals {
			got, err := os.ReadFile(filepath.Join(home, ".cursor", f+".logos-backup"))
			if err != nil {
				t.Fatalf("after setup %d there is no backup of the original %s: %v", i+1, f, err)
			}
			if string(got) != body {
				t.Errorf("after setup %d the backup of %s holds %q, not the user's original", i+1, f, got)
			}
		}
	}
}

// A dangling link where the backup goes used to read as no backup at all, and
// the copy was then written through it to wherever it pointed — outside the
// config directory, under a name the user never chose.
func TestALinkWhereTheBackupGoesIsNotWrittenThrough(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	target := filepath.Join(dir, "elsewhere")
	if err := os.WriteFile(path, []byte("original\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path+".logos-backup"); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	hosts := []Host{{
		Name:     "cli-host",
		Detect:   func() bool { return true },
		Where:    func() string { return "fake mcp add" },
		Config:   func() string { return path },
		Register: func(Server) (Outcome, error) { t.Error("registered without a backup"); return Registered, nil },
	}}

	r := Install(server(), hosts)
	if r[0].Outcome != Failed || r[0].Err == nil || !strings.Contains(r[0].Err.Error(), "left alone") {
		t.Errorf("outcome = %q (%v), want a failure saying the config was left alone", r[0].Outcome, r[0].Err)
	}
	if _, err := os.Stat(target); err == nil {
		t.Error("the backup was written through the link")
	}
}
