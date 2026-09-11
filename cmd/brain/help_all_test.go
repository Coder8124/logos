package main

import (
	"bytes"
	"strings"
	"testing"
)

// helpAll's own promise is that "everything brain has ever accepted is here,
// spelled the way you type it" — it exists so that demoting the general
// surface in helpShort does not amount to hiding it. A flag the parser accepts
// and helpAll omits breaks exactly that promise, and it breaks it invisibly:
// the user has no way to discover the flag except by reading the source.
//
// This is drift, not a typo: each entry here is a flag that was added to a
// parser without the line of help beside it, so the list is a ratchet rather
// than a snapshot — adding a flag to setupCmd or setup.RenderConfig without
// adding it here is exactly the mistake this test exists to catch.
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
