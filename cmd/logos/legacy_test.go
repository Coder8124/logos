package main

import (
	"bytes"
	"os"
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
	if err := os.Mkdir(h+"/brain", 0o700); err != nil {
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

// .brain carries model config and ingest consent, which no reindex rebuilds.
func TestALeftoverBrainStateDirectoryIsNamedButNotMoved(t *testing.T) {
	v := t.TempDir()
	t.Setenv("LOGOS_VAULT", v)
	if err := os.Mkdir(v+"/.brain", 0o700); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	warnOldNames(&stderr)

	if _, err := os.Stat(v + "/.logos"); err == nil {
		t.Error(".brain was moved to .logos at start; 0.5.0 no longer touches it")
	}
	if !strings.Contains(stderr.String(), v+"/.brain") || !strings.Contains(stderr.String(), "mv ") {
		t.Errorf("start did not name .brain and how to keep what is in it:\n%s", stderr.String())
	}
}

// The first command after that warning creates .logos, and .brain's config is
// still only in .brain. Going quiet then would lose it after one mention.
func TestALeftoverBrainStateDirectoryIsStillNamedOnceLogosExists(t *testing.T) {
	v := t.TempDir()
	t.Setenv("LOGOS_VAULT", v)
	for _, d := range []string{"/.brain", "/.logos"} {
		if err := os.Mkdir(v+d, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	var stderr bytes.Buffer
	warnOldNames(&stderr)

	if !strings.Contains(stderr.String(), v+"/.brain") {
		t.Errorf("start said nothing about .brain once .logos existed:\n%s", stderr.String())
	}
}
