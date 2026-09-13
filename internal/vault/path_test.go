package vault

import (
	"os"
	"path/filepath"
	"testing"
)

// isolate points HOME (and its Linux config equivalent) at a scratch directory,
// so a test never reads or writes the developer's real vault pointer.
func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("LOGOS_VAULT", "")
	return home
}

// The desktop app kept its own copy of this rule, defaulting to ~/logos-vault
// while every other front end used ~/logos. index.Open creates the directory it
// is given, so the app made the wrong vault on first launch and then reported a
// healthy zero of everything — a memory product showing an empty screen while
// the memory sat one directory away.
func TestTheDefaultVaultIsLogosInTheHomeDirectory(t *testing.T) {
	home := isolate(t)

	want := filepath.Join(home, "logos")
	if got := Path(); got != want {
		t.Errorf("Path() = %q, want %q", got, want)
	}
}

// The environment still wins, because that is how a host config points the MCP
// server at a vault that is not the default one.
func TestLogosVaultOverridesTheDefault(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	t.Setenv("LOGOS_VAULT", dir)
	if got := Path(); got != dir {
		t.Errorf("Path() = %q, want the LOGOS_VAULT value %q", got, dir)
	}
}

// A relative default would be resolved against whatever directory a host
// happened to launch the binary from, which is the failure the absolute default
// exists to prevent. Pinned so nobody reintroduces it.
func TestTheDefaultIsAbsolute(t *testing.T) {
	isolate(t)
	if p := Path(); !filepath.IsAbs(p) {
		t.Errorf("Path() = %q, which a caller resolves against its own working directory", p)
	}
}

// The point of recording the path: a .app launched from Finder inherits no
// login shell, so LOGOS_VAULT set in a profile is invisible to it. Without a
// written-down location the app can only ever find a vault at the default.
func TestARecordedVaultIsFoundWithNoEnvironment(t *testing.T) {
	isolate(t)
	dir := t.TempDir()

	if err := Record(dir); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if got := Path(); got != dir {
		t.Errorf("Path() = %q, want the recorded vault %q", got, dir)
	}
}

// An explicit instruction in this process beats a choice made on disk some
// other day — otherwise a scratch vault in a test or a per-host MCP config
// would silently open the machine's main vault.
func TestLogosVaultBeatsTheRecordedVault(t *testing.T) {
	isolate(t)
	recorded, env := t.TempDir(), t.TempDir()

	if err := Record(recorded); err != nil {
		t.Fatalf("Record: %v", err)
	}
	t.Setenv("LOGOS_VAULT", env)
	if got := Path(); got != env {
		t.Errorf("Path() = %q, want the LOGOS_VAULT value %q", got, env)
	}
}

// Recording twice moves the pointer rather than accumulating; `logos setup`
// run against a second vault is a change of mind, not an ambiguity.
func TestRecordingAgainMovesThePointer(t *testing.T) {
	isolate(t)
	first, second := t.TempDir(), t.TempDir()

	if err := Record(first); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := Record(second); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if got := Path(); got != second {
		t.Errorf("Path() = %q, want the most recent recorded vault %q", got, second)
	}
}

// A recorded vault that is not there is usually on a drive that is not
// mounted, not gone. Falling back to ~/logos used to be the answer, and it split
// one project across two vaults: the MCP server quietly created ~/logos, said
// "checkpoint saved" into it, and after the drive came back `resume` found
// nothing. The recorded path is still the answer; every caller checks that it
// exists and refuses by name, which is the index.Open problem solved where it
// actually lives.
func TestAVaultThatIsMissingIsStillTheRecordedOne(t *testing.T) {
	isolate(t)
	dir := filepath.Join(t.TempDir(), "unmounted")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := Record(dir); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}

	if got := Path(); got != dir {
		t.Errorf("Path() = %q, want the recorded %q even while it is missing", got, dir)
	}
	if got, explicit := Chosen(); got != dir || !explicit {
		t.Errorf("Chosen() = %q, %v — a recorded vault is a choice someone made, missing or not", got, explicit)
	}
	if got := Pointer(); got != dir {
		t.Errorf("Pointer() = %q, want %q", got, dir)
	}
}

// The pointer records where the vault is, not where the shell happened to be
// standing when setup ran. A relative path written verbatim would resolve
// differently for the app than for the terminal that wrote it.
func TestARecordedPathIsStoredAbsolute(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	t.Chdir(dir)

	if err := Record("."); err != nil {
		t.Fatalf("Record: %v", err)
	}
	got := Recorded()
	if !filepath.IsAbs(got) {
		t.Fatalf("Recorded() = %q, which is relative", got)
	}
	// macOS hands out /var symlinks for temp directories; compare what the
	// filesystem says rather than the spelling.
	if resolved, err := filepath.EvalSymlinks(got); err != nil || resolved != mustResolve(t, dir) {
		t.Errorf("Recorded() = %q, want %q", got, dir)
	}
}

func mustResolve(t *testing.T, dir string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

// Nothing recorded is the normal state on a machine that has never run setup,
// and it must read as "no opinion" rather than as an empty path.
func TestNoRecordedVaultIsNotAnEmptyPath(t *testing.T) {
	isolate(t)
	if got := Recorded(); got != "" {
		t.Errorf("Recorded() = %q on a machine that never ran setup, want \"\"", got)
	}
}
