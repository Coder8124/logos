package mcpserver

import (
	"encoding/json"
	"testing"

	"github.com/Coder8124/brain/internal/buildinfo"
)

// The handshake used to announce a string literal: "0.1.0", written once and
// never touched again. By the v0.1.1 tag it was already a lie, and no test
// noticed because nothing compared it to anything. A host that logs the server
// version, or a user reporting a bug from what their editor shows them, was
// reading a number with no relationship to the binary they were running.
//
// This asserts the one property that keeps it honest: what the server says it
// is, is what the build says it is.
func TestTheHandshakeReportsTheVersionThisBinaryWasBuiltAs(t *testing.T) {
	c, _, _ := startServer(t)
	raw := c.req("initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0"},
	})
	var init struct {
		ServerInfo struct {
			Version string `json:"version"`
		} `json:"serverInfo"`
	}
	if err := json.Unmarshal(raw, &init); err != nil {
		t.Fatalf("initialize result: %v", err)
	}
	if init.ServerInfo.Version != buildinfo.Version {
		t.Errorf("serverInfo.version = %q, want the build's own %q",
			init.ServerInfo.Version, buildinfo.Version)
	}
	// And an unstamped test build must say so rather than inventing a release.
	if init.ServerInfo.Version == "" {
		t.Error("serverInfo.version is empty; a version nobody can read is worse than \"dev\"")
	}
}
