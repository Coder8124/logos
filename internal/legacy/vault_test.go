package legacy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/vault"
)

// home points HOME and the config directory at a scratch directory, so no test
// reads or records the developer's real vault.
func home(t *testing.T) (string, string) {
	t.Helper()
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(h, ".config"))
	unset(t, "LOGOS_VAULT")
	cfg, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	return h, cfg
}

// 0.4 setup recorded the vault under the config directory's brain/. Unread, a
// vault anywhere but the default vanished from the CLI and the desktop app on
// upgrade, and the next write went to an empty ~/logos.
func TestAVaultRecordedUnderTheOldNameIsStillFound(t *testing.T) {
	_, cfg := home(t)
	chosen := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cfg, "brain"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg, "brain", "vault-path"), []byte(chosen+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	notice := Vault()

	if got := vault.Path(); got != chosen {
		t.Errorf("vault.Path() = %q after start, want the vault recorded under brain/ %q", got, chosen)
	}
	if !strings.Contains(notice, chosen) {
		t.Errorf("start did not say which recorded vault it carried over: %q", notice)
	}
}

// The 0.4 default was ~/brain. Someone who never chose a location has their
// memory there, and ~/logos does not exist yet.
func TestAVaultAtTheOldDefaultIsUsedWhenTheNewOneIsAbsent(t *testing.T) {
	h, _ := home(t)
	old := filepath.Join(h, "brain")
	if err := os.Mkdir(old, 0o700); err != nil {
		t.Fatal(err)
	}

	notice := Vault()

	if got := vault.Path(); got != old {
		t.Errorf("vault.Path() = %q after start, want the 0.4 default %q", got, old)
	}
	if !strings.Contains(notice, old) {
		t.Errorf("start did not say it is using the old vault: %q", notice)
	}
}

// A ~/logos that exists is the user's current vault, and a ~/brain beside it is
// something they kept on purpose or forgot; neither is a reason to switch.
func TestAnExistingLogosVaultIsNotReplacedByTheOldOne(t *testing.T) {
	h, _ := home(t)
	for _, d := range []string{"brain", "logos"} {
		if err := os.Mkdir(filepath.Join(h, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}

	if notice := Vault(); notice != "" {
		t.Errorf("start announced %q with ~/logos present", notice)
	}
	if got, want := vault.Path(), filepath.Join(h, "logos"); got != want {
		t.Errorf("vault.Path() = %q, want %q", got, want)
	}
}

// LOGOS_VAULT is this process only, which is how tests and host configs run a
// scratch vault. Recording a location on the strength of such a run would move
// the real machine's vault from under a test.
func TestNothingIsRecordedWhileLogosVaultIsSet(t *testing.T) {
	h, cfg := home(t)
	if err := os.Mkdir(filepath.Join(h, "brain"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOGOS_VAULT", t.TempDir())

	if notice := Vault(); notice != "" {
		t.Errorf("start announced %q with LOGOS_VAULT set", notice)
	}
	if _, err := os.Stat(filepath.Join(cfg, "logos", "vault-path")); err == nil {
		t.Error("a vault location was recorded while LOGOS_VAULT was set")
	}
}
