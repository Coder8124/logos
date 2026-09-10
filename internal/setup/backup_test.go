package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Registering through a host's own CLI rewrites that host's config file just as
// surely as merging it ourselves does — `codex mcp add brain` replaces an
// existing brain entry outright, environment block and all, and says only
// "Added global MCP server 'brain'". Backing up only the hosts brain writes by
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
	if r[0].Backup != path+".brain-backup" {
		t.Errorf("result does not name the backup: %q", r[0].Backup)
	}

	backup, err := os.ReadFile(path + ".brain-backup")
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
	if _, err := os.Stat(path + ".brain-backup"); err == nil {
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
// left a .brain-backup beside each one. Reporting work that did not happen is
// the same fault as swallowing work that did (invariants 3 and 4), and the
// litter it drops is in the user's own config directory.
func TestAHostThatIsAlreadyConnectedIsNotReportedAsChanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	const settled = "{\"mcpServers\":{\"brain\":{}}}\n"
	if err := os.WriteFile(path, []byte(settled), 0o600); err != nil {
		t.Fatal(err)
	}

	hosts := []Host{{
		Name:   "settled",
		Detect: func() bool { return true },
		Where:  func() string { return path },
		Config: func() string { return path },
		// A merge that finds brain already pointed where it should be writes
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
	if _, err := os.Stat(path + ".brain-backup"); err == nil {
		t.Error("left a backup beside a config that was never changed")
	}
}
