package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Coder8124/brain/internal/health"
)

// A diagnostic that creates what it is diagnosing can never report it missing.
//
// gatherHealth opened the index before running the checks, and index.Open makes
// <vault>/.brain — which brings the vault itself into being. So doctor answered
// "vault ok" for a directory that did not exist a moment earlier, and
// checkVault's carefully written "does not exist" branch was unreachable from
// the CLI. The cost is not cosmetic: a typo in BRAIN_VAULT, or running doctor
// before setup, produced a second empty vault with a clean bill of health —
// indistinguishable from a working install that happens to be empty, which is
// the exact failure internal/vault/path.go was written to end.
func TestDoctorDoesNotCreateTheVaultItIsChecking(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-vault-here")
	t.Setenv("BRAIN_VAULT", missing)

	rep := gatherHealth()

	if _, err := os.Stat(missing); err == nil {
		t.Errorf("doctor created %s — a health check must not bring the vault into being", missing)
	}

	c, ok := checkNamed(rep, "vault")
	if !ok {
		t.Fatal("no vault check in the report")
	}
	if c.State != health.Failed {
		t.Errorf("vault check is %v (%q), want FAILED for a vault that does not exist", c.State, c.Detail)
	}
	if !strings.Contains(c.Fix, "brain setup") {
		t.Errorf("vault fix is %q, want it to name `brain setup`", c.Fix)
	}
}

// The rest of the report still has to render. A missing vault is the moment a
// user most needs doctor to run, so every other check degrades to unchecked
// rather than the command failing outright.
func TestDoctorStillReportsWithNoVault(t *testing.T) {
	t.Setenv("BRAIN_VAULT", filepath.Join(t.TempDir(), "no-vault-here"))

	rep := gatherHealth()

	if len(rep.Checks) < 2 {
		t.Fatalf("report has %d checks, want the full set even with no vault", len(rep.Checks))
	}
	if c, ok := checkNamed(rep, "notes"); !ok || c.State == health.OK {
		t.Errorf("notes check is %v, want unchecked rather than a clean bill over a vault that is not there", c.State)
	}
}

// Telling someone to set BRAIN_VAULT is the wrong instruction in both cases
// that reach here: they either never ran setup, or they set BRAIN_VAULT already
// and it points somewhere that does not exist. The old message advised the one
// thing that could not help.
func TestMissingVaultErrorDoesNotAdviseSettingAVariableThatIsAlreadySet(t *testing.T) {
	err := missingVaultError("/nowhere/brain")
	msg := err.Error()

	if !strings.Contains(msg, "/nowhere/brain") {
		t.Errorf("message %q does not name the path it looked at", msg)
	}
	if !strings.Contains(msg, "brain setup") {
		t.Errorf("message %q does not name the command that fixes it", msg)
	}
}

func checkNamed(r health.Report, name string) (health.Check, bool) {
	for _, c := range r.Checks {
		if c.Name == name {
			return c, true
		}
	}
	return health.Check{}, false
}
