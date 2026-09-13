package legacy

import (
	"os"
	"slices"
	"testing"
)

func unset(t *testing.T, key string) {
	t.Helper()
	t.Setenv(key, "")
	os.Unsetenv(key)
}

// A host config written before the rename sets BRAIN_VAULT. Without this the
// server it starts opens ~/logos instead, and the user's memory looks empty.
func TestABrainVariableIsReadUnderItsLogosName(t *testing.T) {
	t.Setenv("BRAIN_VAULT", "/somewhere/vault")
	unset(t, "LOGOS_VAULT")

	got := Env()

	if v := os.Getenv("LOGOS_VAULT"); v != "/somewhere/vault" {
		t.Errorf("LOGOS_VAULT = %q, want the BRAIN_VAULT value", v)
	}
	if !slices.Contains(got, "BRAIN_VAULT") {
		t.Errorf("Env reported %v, which does not name BRAIN_VAULT", got)
	}
}

func TestALogosVariableWinsOverItsBrainName(t *testing.T) {
	t.Setenv("BRAIN_PROJECT", "old")
	t.Setenv("LOGOS_PROJECT", "new")

	if got := Env(); slices.Contains(got, "BRAIN_PROJECT") {
		t.Errorf("Env reported %v for a variable that was already set under its new name", got)
	}
	if v := os.Getenv("LOGOS_PROJECT"); v != "new" {
		t.Errorf("LOGOS_PROJECT = %q, want it left alone", v)
	}
}
