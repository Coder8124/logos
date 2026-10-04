package health

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeLogos writes a shell script at path that answers --version as logos v.
func fakeLogos(t *testing.T, path, v string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho \"logos "+v+" darwin/arm64\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

// brewAt makes prefix the only place a Homebrew logos is looked for, with
// logos-mcp v installed there, and returns its Cellar binary.
func brewAt(t *testing.T, prefix, v string) string {
	t.Helper()
	cellar := fakeLogos(t, filepath.Join(prefix, "Cellar", "logos-mcp", v, "bin", "logos"), v)
	for _, dir := range []string{"opt/logos-mcp/bin", "bin"} {
		if err := os.MkdirAll(filepath.Join(prefix, dir), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(cellar, filepath.Join(prefix, dir, "logos")); err != nil {
			t.Fatal(err)
		}
	}
	old := HomebrewPrefixes
	HomebrewPrefixes = func() []string { return []string{prefix} }
	t.Cleanup(func() { HomebrewPrefixes = old })
	return cellar
}

// Someone who started on npx has a copy pinned in ~/.local/bin, which is ahead
// of Homebrew on PATH. After `brew install`, `logos` still ran the pin, and
// doctor run as it said nothing about a second install.
func TestDoctorRunFromAnOldCopyNamesTheHomebrewInstall(t *testing.T) {
	prefix := t.TempDir()
	brewAt(t, prefix, "0.4.3")
	pin := fakeLogos(t, filepath.Join(t.TempDir(), ".local", "bin", "logos"), "0.4.2")

	c := CheckOtherInstall(pin, "v0.4.2")
	if c.State != Warn || !strings.Contains(c.Detail, "0.4.3") || !strings.Contains(c.Fix, pin) {
		t.Errorf("an old copy with Homebrew's logos installed was not reported: %+v", c)
	}
}

// Doctor run through Homebrew's path works, but `logos` typed in a shell, and
// so `logos setup`, still reached the pin ahead of it on PATH.
func TestDoctorRunFromHomebrewNamesTheCopyAheadOfItOnPath(t *testing.T) {
	prefix := t.TempDir()
	cellar := brewAt(t, prefix, "0.4.3")
	pinDir := filepath.Join(t.TempDir(), ".local", "bin")
	fakeLogos(t, filepath.Join(pinDir, "logos"), "0.4.2")
	t.Setenv("PATH", pinDir+string(os.PathListSeparator)+filepath.Join(prefix, "bin"))

	c := CheckOtherInstall(cellar, "v0.4.3")
	if c.State != Warn || !strings.Contains(c.Detail, filepath.Join(pinDir, "logos")) || !strings.Contains(c.Detail, "0.4.2") {
		t.Errorf("a copy shadowing Homebrew on PATH was not reported: %+v", c)
	}
}

func TestOneHomebrewInstallFirstOnPathReportsNothing(t *testing.T) {
	prefix := t.TempDir()
	cellar := brewAt(t, prefix, "0.4.3")
	t.Setenv("PATH", filepath.Join(prefix, "bin"))

	if c := CheckOtherInstall(cellar, "v0.4.3"); c.Name != "" {
		t.Errorf("a single install was reported as two: %+v", c)
	}
}

// Doctor asks another install its version by running it, and every logos does
// its startup work before it reads its arguments. A 0.4.x logos's startup
// adopts ~/brain, writing the vault pointer and renaming .brain, so a 0.5.0
// doctor next to an old Homebrew logos changed the user's vault by looking.
func TestAskingAnotherInstallItsVersionCannotTouchTheUsersHomeOrVault(t *testing.T) {
	prefix := t.TempDir()
	cellar := filepath.Join(prefix, "Cellar", "logos-mcp", "0.4.9", "bin", "logos")
	brewAt(t, prefix, "0.4.9")
	// The old install's startup, as far as this test cares: it writes into
	// whatever home and vault its environment names, then answers.
	script := "#!/bin/sh\n" +
		"mkdir -p \"$HOME/brain\" && touch \"$HOME/brain/adopted\"\n" +
		"[ -n \"$LOGOS_VAULT\" ] && touch \"$LOGOS_VAULT/touched\"\n" +
		"[ -n \"$BRAIN_VAULT\" ] && touch \"$BRAIN_VAULT/touched\"\n" +
		"echo \"logos v0.4.9 darwin/arm64\"\n"
	if err := os.WriteFile(cellar, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	home, vault := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LOGOS_VAULT", vault)
	t.Setenv("BRAIN_VAULT", vault)
	self := fakeLogos(t, filepath.Join(t.TempDir(), ".local", "bin", "logos"), "0.5.0")

	c := CheckOtherInstall(self, "v0.5.0")
	if !strings.Contains(c.Detail, "0.4.9") {
		t.Fatalf("the Homebrew install's version was not read: %+v", c)
	}
	for _, p := range []string{filepath.Join(home, "brain"), filepath.Join(vault, "touched")} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("running the other install to read its version wrote %s", p)
		}
	}
}
