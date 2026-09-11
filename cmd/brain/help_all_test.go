package main

import (
	"bytes"
	"strings"
	"testing"
)

// helpAll's own promise is that "everything brain has ever accepted is here,
// spelled the way you type it" — it exists so that demoting the general surface
// in helpShort does not amount to hiding it. A flag the parser accepts and
// helpAll omits breaks exactly that promise, and it breaks it invisibly: the
// user has no way to discover the flag except by reading the source.
//
// This is drift, not a typo. Each of these was added to a parser without the
// one line of help beside it, so the list is a ratchet rather than a snapshot:
// adding a flag means adding it here too.
func TestHelpAllNamesEveryFlagTheParsersAccept(t *testing.T) {
	var b bytes.Buffer
	helpAll(&b)
	help := b.String()

	for _, want := range []struct{ verb, flag string }{
		// checkpoint takes thirteen flags; helpAll listed five.
		{"checkpoint", "--state"},
		{"checkpoint", "--decided"},
		{"checkpoint", "--verified"},
		{"checkpoint", "--blocker"},
		{"checkpoint", "--ran"},
		{"checkpoint", "--question"},
		{"checkpoint", "--file"},
		// ingest --path is worse than undocumented: ingest's own error message
		// tells the user "--path needs --harness", naming a flag help never did.
		{"ingest", "--path"},
		{"bootstrap", "--dir"},
		{"why", "--limit"},
		// Not flags but whole verbs, and the shipped context-connect skill tells
		// users to run both — so they were discoverable from the plugin and not
		// from brain itself.
		{"project-name", "project-name"},
		{"project rename", "project rename"},
	} {
		if !strings.Contains(help, want.flag) {
			t.Errorf("brain %s accepts %s, and `brain help all` never says so", want.verb, want.flag)
		}
	}

	// memory dispatches eleven subcommands; helpAll listed six. Searched within
	// the memory lines rather than the whole page: "health" also appears in
	// doctor's description, which would pass this check while `brain memory
	// health` stayed undocumented.
	var mem string
	for _, line := range strings.Split(help, "\n") {
		if strings.Contains(line, "brain memory") {
			mem += line + "\n"
		}
	}
	for _, sub := range []string{"health", "consolidate", "pin", "unpin", "exclude"} {
		if !strings.Contains(mem, sub) {
			t.Errorf("brain memory %s works, and `brain help all` never says so", sub)
		}
	}
}

// The same ratchet, for the flags that make brain installable on an MCP client
// setup.Hosts() has never heard of. Adding a flag to setupCmd or
// setup.RenderConfig without a line of help beside it hides the one route a
// user of a fifth host has.
func TestHelpAllNamesTheUniversalMCPInstallFlags(t *testing.T) {
	var b bytes.Buffer
	helpAll(&b)
	help := b.String()

	for _, want := range []string{
		// The escape hatch for any MCP client that is not one of the four
		// setup.Hosts() knows how to find or register.
		"--print-config",
		"--format",
		// Merges into a config file at a location brain has no built-in
		// convention for, reusing the same merge Claude Desktop and Cursor get.
		"--config",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("brain setup accepts %s, and `brain help all` never says so", want)
		}
	}
}
