package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// There was no way to take logos back out of a host short of editing its
// config by hand, and someone trying it out had no clean exit.
func TestUninstallTakesLogosOutOfAHostAndKeepsTheRest(t *testing.T) {
	fakeHome(t, ".cursor")
	h := cursor()
	cfg := `{"mcpServers": {"logos": {"command": "/usr/local/bin/logos", "args": ["mcp", "serve"]}, "brain": {"command": "/Users/x/.local/bin/brain", "args": ["mcp", "serve"]}, "theirs": {"command": "theirs"}}}`
	if err := os.WriteFile(h.Config(), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	r := Uninstall([]Host{h})

	servers := readServers(t, h.Config())
	for _, name := range []string{Name, OldName} {
		if _, ok := servers[name]; ok {
			t.Errorf("the %s entry is still there:\n%+v", name, servers)
		}
	}
	if _, ok := servers["theirs"]; !ok {
		t.Errorf("the user's other server was lost:\n%+v", servers)
	}
	if len(r) != 1 || r[0].Err != nil || strings.Join(r[0].Removed, ",") != "logos,brain" {
		t.Errorf("result = %+v, want logos and brain removed", r)
	}
	if raw, err := os.ReadFile(r[0].Backup); err != nil || string(raw) != cfg {
		t.Errorf("the config was not backed up before it was changed (%v)", err)
	}
}

// A host with nothing of ours in it is reported as such, and gets no backup
// file nobody needs.
func TestUninstallFromAHostWithoutLogosChangesNothing(t *testing.T) {
	fakeHome(t, ".cursor")
	h := cursor()
	cfg := `{"mcpServers": {"brain": {"command": "uvx", "args": ["some-other-brain"]}}}`
	if err := os.WriteFile(h.Config(), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	r := Uninstall([]Host{h})

	if raw, _ := os.ReadFile(h.Config()); string(raw) != cfg {
		t.Errorf("a config with no logos entry was rewritten:\n%s", raw)
	}
	if len(r[0].Removed) != 0 || r[0].Err != nil || r[0].Backup != "" {
		t.Errorf("result = %+v, want nothing removed and no backup", r[0])
	}
	if _, err := os.Stat(h.Config() + ".logos-backup"); err == nil {
		t.Error("a backup was left beside a config that did not change")
	}
}

func TestUninstallRemovesLogosFromClaudeCode(t *testing.T) {
	_, calls := stubCLI(t, "claude", `
[ "$2" = list ] && echo "logos: /usr/local/bin/logos mcp serve - ✓ Connected"
exit 0
`)

	r := Uninstall([]Host{claudeCode()})

	log, _ := os.ReadFile(calls)
	if !strings.Contains(string(log), "mcp remove --scope user logos") {
		t.Errorf("the logos entry was not removed; calls:\n%s", log)
	}
	if len(r[0].Removed) != 1 || r[0].Removed[0] != Name || r[0].Err != nil {
		t.Errorf("result = %+v, want logos removed", r[0])
	}
}

// A second uninstall found nothing to remove and deleted its backup, which was
// written over the first run's: the one copy of the config with logos in it.
func TestASecondUninstallKeepsTheFirstBackup(t *testing.T) {
	fakeHome(t, ".cursor")
	h := cursor()
	cfg := `{"mcpServers": {"logos": {"command": "/usr/local/bin/logos", "args": ["mcp", "serve"]}}}`
	if err := os.WriteFile(h.Config(), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	first := Uninstall([]Host{h})
	Uninstall([]Host{h})

	if raw, err := os.ReadFile(first[0].Backup); err != nil || string(raw) != cfg {
		t.Errorf("the first uninstall's backup was lost (%v): %q", err, raw)
	}
}

// Setup keeps the user's config from before logos as .logos-backup and never
// writes over it (#353), but uninstall renamed its own copy over that name, so
// setup then uninstall left only copies with logos in them.
func TestUninstallKeepsTheBackupOfTheUsersOriginalConfig(t *testing.T) {
	home := fakeHome(t, ".cursor")
	cursor := hostNamed(t, "Cursor")
	path := filepath.Join(home, ".cursor", "mcp.json")
	const original = "{\"mcpServers\":{\"theirs\":{\"command\":\"their-server\"}}}\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if r := Install(server(), []Host{cursor}); r[0].Outcome == Failed {
		t.Fatalf("setup: %v", r[0].Err)
	}
	connected, _ := os.ReadFile(path)

	r := Uninstall([]Host{cursor})

	if r[0].Err != nil || len(r[0].Removed) == 0 {
		t.Fatalf("uninstall = %+v", r[0])
	}
	if got, _ := os.ReadFile(path + ".logos-backup"); string(got) != original {
		t.Errorf("the backup of the user's original config now holds %q", got)
	}
	if got, err := os.ReadFile(r[0].Backup); err != nil || string(got) != string(connected) {
		t.Errorf("the config as uninstall found it was not kept at %q (%v): %q", r[0].Backup, err, got)
	}
	if r[0].Original != path+".logos-backup" {
		t.Errorf("original = %q, want the .logos-backup named", r[0].Original)
	}
}
