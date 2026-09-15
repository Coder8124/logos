package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/health"
	"github.com/Coder8124/logos/internal/setup"
	"github.com/Coder8124/logos/internal/vault"
)

// fakeHosts replaces host detection and the integration self-test for the
// duration of one test. Nothing here may touch a real config or run a real
// host CLI: an earlier version of this test reached setup.Hosts() and actually
// ran `claude mcp add --scope user` and `codex mcp add` on the developer's
// machine. Only a faked HOME kept it inside a temp directory.
func fakeHosts(t *testing.T, registered ...string) {
	t.Helper()
	var hosts []setup.Host
	for _, name := range registered {
		hosts = append(hosts, setup.Host{
			Name:     name,
			Detect:   func() bool { return true },
			Where:    func() string { return "/nowhere/config.json" },
			Register: func(setup.Server) (setup.Outcome, error) { return setup.Registered, nil },
		})
	}
	old, oldCheck := detectHosts, integrationChecks
	detectHosts = func() []setup.Host { return hosts }
	integrationChecks = func(string, []string, string) []health.Check {
		return []health.Check{{Name: "handshake", State: health.OK, Detail: "faked"}}
	}
	t.Cleanup(func() { detectHosts, integrationChecks = old, oldCheck })
}

func setupInFakeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	dir := filepath.Join(t.TempDir(), "vault")
	t.Setenv("LOGOS_VAULT", dir)
	// Setup declines to wire hosts to a temporary vault nobody recorded, and
	// t.TempDir is one. Moving TMPDIR makes this vault an ordinary directory, so
	// the tests built on it exercise the prompts they are about.
	tmp := filepath.Join(home, "tmp")
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", tmp)
	return dir
}

// The roster of hosts is what a person says yes or no to. With --yes there is
// no prompt, so printing it and then printing the same rows again with their
// outcomes made setup — the first screen a new user sees — look like it had
// rendered its output twice.
func TestTheHostListIsNotPrintedTwiceWhenThereIsNothingToConfirm(t *testing.T) {
	dir := setupInFakeHome(t)
	fakeHosts(t, "Fakey Desktop")

	out := captureStdout(t, func() {
		if err := setupCmd([]string{"--vault", dir, "--yes"}); err != nil {
			t.Fatalf("setup: %v", err)
		}
	})

	// Counted as roster rows, not mentions: the closing steps name the host
	// on purpose.
	if n := len(regexp.MustCompile(`(?m)^\s+Fakey Desktop\s{2,}`).FindAllString(out, -1)); n != 1 {
		t.Errorf("host listed %d times, want 1:\n%s", n, out)
	}
	if !strings.Contains(out, "registered") {
		t.Errorf("the one line kept must be the outcome, not the plan:\n%s", out)
	}
}

// --dry-run is nothing but the roster, so there it stays.
func TestADryRunStillShowsWhichHostsItWouldWire(t *testing.T) {
	dir := setupInFakeHome(t)
	fakeHosts(t, "Fakey Desktop")

	out := captureStdout(t, func() {
		if err := setupCmd([]string{"--vault", dir, "--dry-run"}); err != nil {
			t.Fatalf("setup: %v", err)
		}
	})

	if !strings.Contains(out, "Fakey Desktop") {
		t.Errorf("dry run did not name the host it would wire:\n%s", out)
	}
	if !strings.Contains(out, "nothing was written") {
		t.Errorf("dry run did not say it wrote nothing:\n%s", out)
	}
}

// The README offers the plugin and `npx … setup` one after the other, and a
// person who does both gets logos registered twice in Claude Code: every tool
// listed twice, the fixed per-session cost paid twice. Setup had no idea the
// plugin existed. With it installed, Claude Code is already connected.
func TestSetupDoesNotRegisterClaudeCodeAgainWhenThePluginIsInstalled(t *testing.T) {
	dir := setupInFakeHome(t)
	fakeHosts(t, "Claude Code", "Fakey Desktop")
	home := os.Getenv("HOME")
	plugins := filepath.Join(home, ".claude", "plugins")
	if err := os.MkdirAll(plugins, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `{"version":2,"plugins":{"logos@logos":[{"scope":"user","version":"0.4.2"}]}}`
	if err := os.WriteFile(filepath.Join(plugins, "installed_plugins.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		if err := setupCmd([]string{"--vault", dir, "--yes"}); err != nil {
			t.Fatalf("setup: %v", err)
		}
	})

	if strings.Contains(out, fmt.Sprintf("%-*s ✓", hostColumn, "Claude Code")) {
		t.Errorf("Claude Code was registered on top of the plugin:\n%s", out)
	}
	if !strings.Contains(out, "Logos plugin") {
		t.Errorf("setup skipped Claude Code without saying why:\n%s", out)
	}
	if !strings.Contains(out, fmt.Sprintf("%-*s ✓", hostColumn, "Fakey Desktop")) {
		t.Errorf("the other hosts must still be wired:\n%s", out)
	}
}

// With Claude Code the only host and the plugin already connecting it, there
// is nothing left to wire — and "No MCP hosts found. Install Claude Code" right
// under the line saying Claude Code is connected would be false.
func TestSetupWithOnlyThePluginDoesNotSayNoHostsWereFound(t *testing.T) {
	dir := setupInFakeHome(t)
	fakeHosts(t, "Claude Code")
	plugins := filepath.Join(os.Getenv("HOME"), ".claude", "plugins")
	if err := os.MkdirAll(plugins, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `{"version":2,"plugins":{"logos@logos":[{"scope":"user","version":"0.4.2"}]}}`
	if err := os.WriteFile(filepath.Join(plugins, "installed_plugins.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		if err := setupCmd([]string{"--vault", dir, "--yes"}); err != nil {
			t.Fatalf("setup: %v", err)
		}
	})

	if strings.Contains(out, "No MCP hosts found") {
		t.Errorf("setup said no hosts were found while the plugin connects Claude Code:\n%s", out)
	}
	if !strings.Contains(out, "Logos plugin") {
		t.Errorf("setup must still say the plugin connects Claude Code:\n%s", out)
	}
}

// Setup only runs `claude mcp add`, which gives Claude Code the tools but none
// of the plugin's hooks — nothing restores the last checkpoint at session
// start, so "resume" happens only if the model thinks to call it. The README
// calls the plugin the version to prefer; setup never said it existed.
func TestSetupPointsAClaudeCodeUserWithoutThePluginAtIt(t *testing.T) {
	dir := setupInFakeHome(t)
	fakeHosts(t, "Claude Code")

	out := captureStdout(t, func() {
		if err := setupCmd([]string{"--vault", dir, "--yes"}); err != nil {
			t.Fatalf("setup: %v", err)
		}
	})

	if !strings.Contains(out, "/plugin install logos@logos") {
		t.Errorf("setup did not mention the plugin to a Claude Code user:\n%s", out)
	}
}

// The hint is for Claude Code users. Someone wiring only Cursor has no use
// for a Claude Code slash command.
func TestSetupDoesNotMentionThePluginWhenClaudeCodeIsNotWired(t *testing.T) {
	dir := setupInFakeHome(t)
	fakeHosts(t, "Fakey Desktop")

	out := captureStdout(t, func() {
		if err := setupCmd([]string{"--vault", dir, "--yes"}); err != nil {
			t.Fatalf("setup: %v", err)
		}
	})

	if strings.Contains(out, "/plugin") {
		t.Errorf("setup mentioned the Claude Code plugin with no Claude Code wired:\n%s", out)
	}
}

// A second entry — a 0.4 `brain` one setup could not remove, or one written by
// hand. Setup added `logos`
// beside it and printed only `✓ registered`, so the moment the duplicate was
// created was also the moment nothing mentioned it.
func TestSetupNamesAnotherLogosEntryAlreadyInTheHost(t *testing.T) {
	dir := setupInFakeHome(t)
	fakeHosts(t, "Fakey Cursor")
	hosts := detectHosts()
	hosts[0].List = func() ([]setup.Registration, error) {
		return []setup.Registration{
			{Name: "brain", Command: "npx -y @noeton/logos mcp serve"},
			{Name: "logos", Command: "/usr/local/bin/logos mcp serve"},
			{Name: "github", Command: "github-mcp"},
		}, nil
	}
	detectHosts = func() []setup.Host { return hosts }

	out := captureStdout(t, func() {
		if err := setupCmd([]string{"--vault", dir, "--yes"}); err != nil {
			t.Fatalf("setup: %v", err)
		}
	})

	if !strings.Contains(out, "brain") || !strings.Contains(out, "twice") {
		t.Errorf("setup did not name the other logos entry it sat beside:\n%s", out)
	}
	if strings.Contains(out, "github") {
		t.Errorf("setup named an unrelated server as a duplicate:\n%s", out)
	}
}

// Removing the entry 0.4 setup wrote changes someone's host config, so it is
// said; a removal that failed is said too, with the error, since that host now
// loads logos twice.
func TestSetupSaysItReplacedTheOldBrainEntryOrCouldNot(t *testing.T) {
	dir := setupInFakeHome(t)
	fakeHosts(t, "Fakey Cursor", "Fakey Codex")
	hosts := detectHosts()
	hosts[0].Remove = func(string) (bool, error) { return true, nil }
	hosts[1].Remove = func(string) (bool, error) { return false, fmt.Errorf("codex: permission denied") }
	detectHosts = func() []setup.Host { return hosts }

	out := captureStdout(t, func() {
		if err := setupCmd([]string{"--vault", dir, "--yes"}); err != nil {
			t.Fatalf("setup: %v", err)
		}
	})

	if !strings.Contains(out, "replaced the brain entry") {
		t.Errorf("setup did not say it replaced the brain entry:\n%s", out)
	}
	if !strings.Contains(out, "permission denied") {
		t.Errorf("setup did not report the removal that failed:\n%s", out)
	}
}

// `logos setup --vault B --host cursor` moved the machine's recorded vault to B
// and wired Cursor there, while Claude Desktop stayed on A. Setup said only
// "recorded", so checkpoints written from one host were invisible to the other
// with nothing on screen tying it back to that run.
func TestSetupNamesTheHostsLeftOnThePreviousVault(t *testing.T) {
	dir := setupInFakeHome(t)
	old := t.TempDir()
	if err := vault.Record(old); err != nil {
		t.Fatal(err)
	}
	fakeHosts(t, "Fakey Cursor", "Fakey Desktop")
	hosts := detectHosts()
	hosts[1].List = func() ([]setup.Registration, error) {
		return []setup.Registration{{Name: "logos", Command: "/usr/local/bin/logos mcp serve", Vault: old}}, nil
	}
	detectHosts = func() []setup.Host { return hosts }

	out := captureStdout(t, func() {
		if err := setupCmd([]string{"--vault", dir, "--host", "fakey-cursor", "--yes"}); err != nil {
			t.Fatalf("setup: %v", err)
		}
	})

	if !strings.Contains(out, "Fakey Desktop") || !strings.Contains(out, old) {
		t.Errorf("setup moved the vault without naming the host still on the old one:\n%s", out)
	}
}

// A dry run records nothing, so nothing moved and no host was left behind.
func TestADryRunDoesNotSayTheVaultMoved(t *testing.T) {
	dir := setupInFakeHome(t)
	old := t.TempDir()
	if err := vault.Record(old); err != nil {
		t.Fatal(err)
	}
	fakeHosts(t, "Fakey Desktop")
	hosts := detectHosts()
	hosts[0].List = func() ([]setup.Registration, error) {
		return []setup.Registration{{Name: "logos", Command: "/usr/local/bin/logos mcp serve", Vault: old}}, nil
	}
	detectHosts = func() []setup.Host { return hosts }

	out := captureStdout(t, func() {
		if err := setupCmd([]string{"--vault", dir, "--dry-run", "--yes"}); err != nil {
			t.Fatalf("setup: %v", err)
		}
	})

	if strings.Contains(out, "moved from") {
		t.Errorf("a dry run said the vault moved:\n%s", out)
	}
}

// The README's source route cloned logos into ~ and ran setup, and the clone
// was ~/logos — setup's default vault. Setup took the checkout without a word,
// indexed the repository's own markdown as notes, and would have written the
// user's memory inside a tree a `git clean` deletes.
func TestSetupRefusesASourceCheckoutNobodyChoseAsTheVault(t *testing.T) {
	setupInFakeHome(t)
	t.Setenv("LOGOS_VAULT", "")
	fakeHosts(t, "Fakey Cursor")
	checkout := filepath.Join(os.Getenv("HOME"), "logos")
	if err := os.MkdirAll(checkout, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checkout, "go.mod"), []byte("module github.com/Coder8124/logos\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var err error
	captureStdout(t, func() { err = setupCmd([]string{"--yes"}) })

	if err == nil || !strings.Contains(err.Error(), "--vault") {
		t.Fatalf("setup took a source checkout as the vault: err=%v", err)
	}
	if _, statErr := os.Stat(filepath.Join(checkout, "sessions")); statErr == nil {
		t.Errorf("setup wrote into the checkout before refusing it")
	}
	if vault.Pointer() != "" {
		t.Errorf("setup recorded the checkout as this machine's vault: %s", vault.Pointer())
	}
}

// A vault that already holds memory is a vault, whatever else sits in it.
func TestSetupKeepsAVaultWithHistoryEvenIfItHoldsAGoModule(t *testing.T) {
	setupInFakeHome(t)
	t.Setenv("LOGOS_VAULT", "")
	fakeHosts(t, "Fakey Cursor")
	dir := filepath.Join(os.Getenv("HOME"), "logos")
	if err := os.MkdirAll(filepath.Join(dir, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	captureStdout(t, func() {
		if err := setupCmd([]string{"--yes"}); err != nil {
			t.Fatalf("setup refused a vault that already has sessions: %v", err)
		}
	})
}

// installPluginRecord writes Claude Code's installed-plugins record, and its
// settings when given, into the fake HOME.
func installPluginRecord(t *testing.T, manifest, settings string) {
	t.Helper()
	claude := filepath.Join(os.Getenv("HOME"), ".claude")
	if err := os.MkdirAll(filepath.Join(claude, "plugins"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claude, "plugins", "installed_plugins.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if settings != "" {
		if err := os.WriteFile(filepath.Join(claude, "settings.json"), []byte(settings), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// With the plugin turned off, setup still said "already connected by the
// Logos plugin" and wired nothing, leaving Claude Code with no Logos.
func TestSetupWiresClaudeCodeWhenTheLogosPluginIsDisabled(t *testing.T) {
	dir := setupInFakeHome(t)
	fakeHosts(t, "Claude Code")
	installPluginRecord(t, `{"version":2,"plugins":{"logos@logos":[{"scope":"user","version":"0.4.3"}]}}`, `{"enabledPlugins":{"logos@logos":false}}`)

	out := captureStdout(t, func() {
		if err := setupCmd([]string{"--vault", dir, "--yes"}); err != nil {
			t.Fatalf("setup: %v", err)
		}
	})
	if !strings.Contains(out, fmt.Sprintf("%-*s ✓", hostColumn, "Claude Code")) {
		t.Errorf("Claude Code was not wired though the plugin is disabled:\n%s", out)
	}
	if !strings.Contains(out, "disabled") {
		t.Errorf("setup did not say why it wired Claude Code despite the plugin:\n%s", out)
	}
}

// A plugin older than the binary runs old hooks against a new server, and the
// setup run that skipped Claude Code because of it said nothing.
func TestSetupSaysHowToUpdateAPluginOlderThanThisLogos(t *testing.T) {
	dir := setupInFakeHome(t)
	fakeHosts(t, "Claude Code")
	installPluginRecord(t, `{"version":2,"plugins":{"logos@logos":[{"scope":"user","version":"0.1.2"}]}}`, "")
	old := version
	version = "v0.4.3"
	t.Cleanup(func() { version = old })

	out := captureStdout(t, func() {
		if err := setupCmd([]string{"--vault", dir, "--yes"}); err != nil {
			t.Fatalf("setup: %v", err)
		}
	})
	if !strings.Contains(out, "claude plugin update logos@logos") {
		t.Errorf("setup skipped an outdated plugin without saying how to update it:\n%s", out)
	}
}
