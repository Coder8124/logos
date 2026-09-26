package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/vault"
)

// 0.5.0 reads no BRAIN_ name, and a profile that still exports one has to hear
// that, or its vault override quietly stops applying.
func TestABrainVariableIsNotReadButIsNamedWithItsReplacement(t *testing.T) {
	t.Setenv("BRAIN_VAULT", "/somewhere/vault")
	t.Setenv("LOGOS_VAULT", "")
	os.Unsetenv("LOGOS_VAULT")

	var stderr bytes.Buffer
	warnOldNames(&stderr)

	if v, set := os.LookupEnv("LOGOS_VAULT"); set {
		t.Errorf("LOGOS_VAULT = %q after start; the old name must no longer be read", v)
	}
	if !strings.Contains(stderr.String(), "BRAIN_VAULT") || !strings.Contains(stderr.String(), "LOGOS_VAULT") {
		t.Errorf("start did not name the old variable and its new name:\n%s", stderr.String())
	}
}

// A machine that skipped every 0.4 release that recorded ~/brain would open an
// empty ~/logos. Adopting it is what 0.5.0 stopped doing; saying so is not.
func TestAnUnrecordedBrainVaultIsNamedButNotAdopted(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("XDG_CONFIG_HOME", h+"/.config")
	t.Setenv("LOGOS_VAULT", "")
	os.Unsetenv("LOGOS_VAULT")
	if err := os.MkdirAll(h+"/brain/.brain", 0o700); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	warnOldNames(&stderr)

	if p := vault.Pointer(); p != "" {
		t.Errorf("start recorded %s as this machine's vault; only setup or migrate may", p)
	}
	if !strings.Contains(stderr.String(), h+"/brain") || !strings.Contains(stderr.String(), "logos migrate") {
		t.Errorf("start did not name the old vault and the command that moves it:\n%s", stderr.String())
	}
}

// The first run after the upgrade is usually a host's MCP server, whose
// stderr nobody reads, and it creates an empty ~/logos. A warning that stopped
// once ~/logos existed was said once, to nobody, and the user then saw a vault
// with nothing in it — and migrate, the fix it named, refuses once ~/logos is
// there.
func TestAnUnrecordedBrainVaultIsStillNamedOnceAnEmptyLogosExists(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("LOGOS_VAULT", "")
	os.Unsetenv("LOGOS_VAULT")
	for _, d := range []string{"/brain/.brain", "/logos/.logos"} {
		if err := os.MkdirAll(h+d, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	var stderr bytes.Buffer
	warnOldNames(&stderr)

	if !strings.Contains(stderr.String(), "logos setup --vault "+h+"/brain") {
		t.Errorf("start did not name the old vault and how to keep using it once ~/logos existed:\n%s", stderr.String())
	}
	if strings.Contains(stderr.String(), "logos migrate") {
		t.Errorf("start offered migrate, which refuses once ~/logos exists:\n%s", stderr.String())
	}
}

// 0.4 could record any directory. migrate moves only ~/brain, so offering it
// for a vault elsewhere either fails or moves a different, stale ~/brain.
func TestAVaultThat04RecordedElsewhereIsNotOfferedMigrate(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("XDG_CONFIG_HOME", h+"/.config")
	t.Setenv("LOGOS_VAULT", "")
	os.Unsetenv("LOGOS_VAULT")
	cfg, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	notes := filepath.Join(h, "notes")
	for _, d := range []string{filepath.Join(cfg, "brain"), notes} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(cfg, "brain", "vault-path"), []byte(notes+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	warnOldNames(&stderr)

	if !strings.Contains(stderr.String(), "logos setup --vault "+notes) {
		t.Errorf("start did not name the recorded 0.4 vault and how to keep it:\n%s", stderr.String())
	}
	if strings.Contains(stderr.String(), "logos migrate") {
		t.Errorf("start offered migrate for a vault that is not ~/brain:\n%s", stderr.String())
	}
}

// "brain" is a common name for somebody's own notes, and this runs on every
// command until a vault is recorded.
func TestAFolderCalledBrainThatWasNeverAVaultIsNotNamed(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("XDG_CONFIG_HOME", h+"/.config")
	t.Setenv("LOGOS_VAULT", "")
	os.Unsetenv("LOGOS_VAULT")
	if err := os.Mkdir(h+"/brain", 0o700); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	warnOldNames(&stderr)

	if strings.Contains(stderr.String(), h+"/brain") {
		t.Errorf("start named a folder that holds no 0.4 index as a vault:\n%s", stderr.String())
	}
}

// .brain carries model config and ingest consent, which no reindex rebuilds.
// The command the warning gives is run as given, in both states it is said
// in: before and after the first command has created .logos. `mv .brain
// .logos` was right for the first and, for the second, nested .brain inside
// .logos where nothing reads it.
func TestTheCommandNamedForALeftoverBrainDirectoryKeepsItsConfig(t *testing.T) {
	for _, logosExists := range []bool{false, true} {
		v := t.TempDir()
		t.Setenv("LOGOS_VAULT", v)
		if err := os.Mkdir(v+"/.brain", 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(v+"/.brain/config.json", []byte(`{"t1":"x"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if logosExists {
			if err := os.Mkdir(v+"/.logos", 0o700); err != nil {
				t.Fatal(err)
			}
		}

		var stderr bytes.Buffer
		warnOldNames(&stderr)

		if _, err := os.Stat(v + "/.logos/config.json"); err == nil {
			t.Fatal("start copied .brain's config itself; 0.5.0 no longer touches it")
		}
		m := regexp.MustCompile("`([^`]*" + regexp.QuoteMeta(v) + "/.brain[^`]*)`").FindStringSubmatch(stderr.String())
		if m == nil {
			t.Fatalf("with .logos present=%v, start did not name .brain with a command:\n%s", logosExists, stderr.String())
		}
		if out, err := exec.Command("sh", "-c", m[1]).CombinedOutput(); err != nil {
			t.Fatalf("the named command %q failed: %v\n%s", m[1], err, out)
		}
		if b, err := os.ReadFile(v + "/.logos/config.json"); err != nil || string(b) != `{"t1":"x"}` {
			t.Errorf("with .logos present=%v, %q did not leave the config where logos reads it: %v", logosExists, m[1], err)
		}
	}
}

// Inside a host the run uses the vault that host pins, so that is the vault
// whose leftovers matter. Checked before the pin was adopted, a .brain there
// was never named, and a ~/brain was named to a run about to use neither.
func TestOldNamesAreCheckedAgainstTheVaultTheHostPins(t *testing.T) {
	h, pinned := t.TempDir(), t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("XDG_CONFIG_HOME", h+"/.config")
	t.Setenv("LOGOS_HOST", "claude-code")
	t.Setenv("LOGOS_VAULT", "")
	os.Unsetenv("LOGOS_VAULT")
	for _, d := range []string{h + "/brain/.brain", pinned + "/.brain"} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	var stderr bytes.Buffer
	start(&stderr, pinnedHosts(t, pinned))

	if !strings.Contains(stderr.String(), pinned+"/.brain") {
		t.Errorf("the pinned vault's .brain was not named:\n%s", stderr.String())
	}
	if strings.Contains(stderr.String(), h+"/brain ") {
		t.Errorf("~/brain was named to a run that uses the host's pinned vault:\n%s", stderr.String())
	}
}
