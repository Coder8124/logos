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
	t.Setenv("BRAIN_VAULT", "")
	return home
}

// The desktop app kept its own copy of this rule, defaulting to ~/brain-vault
// while every other front end used ~/brain. index.Open creates the directory it
// is given, so the app made the wrong vault on first launch and then reported a
// healthy zero of everything — a memory product showing an empty screen while
// the memory sat one directory away.
func TestTheDefaultVaultIsBrainInTheHomeDirectory(t *testing.T) {
	home := isolate(t)

	want := filepath.Join(home, "brain")
	if got := Path(); got != want {
		t.Errorf("Path() = %q, want %q", got, want)
	}
}

// The environment still wins, because that is how a host config points the MCP
// server at a vault that is not the default one.
func TestBrainVaultOverridesTheDefault(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	t.Setenv("BRAIN_VAULT", dir)
	if got := Path(); got != dir {
		t.Errorf("Path() = %q, want the BRAIN_VAULT value %q", got, dir)
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
// login shell, so BRAIN_VAULT set in a profile is invisible to it. Without a
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
func TestBrainVaultBeatsTheRecordedVault(t *testing.T) {
	isolate(t)
	recorded, env := t.TempDir(), t.TempDir()

	if err := Record(recorded); err != nil {
		t.Fatalf("Record: %v", err)
	}
	t.Setenv("BRAIN_VAULT", env)
	if got := Path(); got != env {
		t.Errorf("Path() = %q, want the BRAIN_VAULT value %q", got, env)
	}
}

// Recording twice moves the pointer rather than accumulating; `brain setup`
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

// If the recorded vault is gone, honouring it would point every front end at a
// directory that does not exist — and index.Open would helpfully create it,
// which is how the empty-vault bug happened the first time.
func TestAVaultThatHasBeenDeletedIsNotHonoured(t *testing.T) {
	home := isolate(t)
	dir := filepath.Join(t.TempDir(), "moved-away")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := Record(dir); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}

	want := filepath.Join(home, "brain")
	if got := Path(); got != want {
		t.Errorf("Path() = %q, want the default %q once the recorded vault is gone", got, want)
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
