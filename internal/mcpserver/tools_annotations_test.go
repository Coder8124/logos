package mcpserver

import (
	"encoding/json"
	"testing"
)

// Tool annotations are the protocol's only way to say "this tool just reads",
// and without them a host has to assume every tool writes. That is what made
// the whole server unavailable in an editor's read-only chat mode — including
// resume, which is the tool you most want there, because "where did we leave
// off" is a question you ask before touching anything.
func TestReadToolsDeclareThemselvesReadOnly(t *testing.T) {
	readers := map[string]bool{
		"recall": true, "list_memories": true, "context": true, "resume": true,
		"before_you_try": true, "why": true, "memory_diff": true, "list_projects": true,
	}
	seen := map[string]bool{}
	for _, def := range toolDefs {
		name, _ := def["name"].(string)
		ann, ok := def["annotations"].(map[string]any)
		if !ok {
			t.Errorf("tool %q carries no annotations, so a host cannot tell whether it writes", name)
			continue
		}
		if ann["readOnlyHint"] != readers[name] {
			t.Errorf("tool %q: readOnlyHint = %v, want %v", name, ann["readOnlyHint"], readers[name])
		}
		// Everything here touches one directory on the user's own disk.
		if ann["openWorldHint"] != false {
			t.Errorf("tool %q claims to reach outside the machine", name)
		}
		if readers[name] && ann["destructiveHint"] == true {
			t.Errorf("tool %q is marked both read-only and destructive", name)
		}
		seen[name] = true
	}
	for name := range readers {
		if !seen[name] {
			t.Errorf("read tool %q is missing from toolDefs", name)
		}
	}
}

// forget is the one tool that removes something the user already had, and a
// host deciding whether to confirm a call needs to be told which one that is.
func TestForgetIsTheDestructiveOne(t *testing.T) {
	for _, def := range toolDefs {
		ann, _ := def["annotations"].(map[string]any)
		want := def["name"] == "forget"
		if got := ann["destructiveHint"] == true; got != want {
			t.Errorf("tool %q: destructiveHint = %v, want %v", def["name"], got, want)
		}
	}
}

// The specification asks a server to echo a revision it speaks and otherwise
// answer with the newest it does. Pinning one hard-coded string instead is how
// the server ended up telling every host it was speaking a revision from before
// annotations existed.
func TestProtocolVersionIsNegotiated(t *testing.T) {
	for _, tc := range []struct{ asked, want string }{
		{"2024-11-05", "2024-11-05"},
		{"2025-03-26", "2025-03-26"},
		{"2025-06-18", "2025-06-18"},
		{"2099-01-01", protocolVersion},
		{"", protocolVersion},
	} {
		params, err := json.Marshal(map[string]any{"protocolVersion": tc.asked})
		if err != nil {
			t.Fatal(err)
		}
		if got := negotiateVersion(params); got != tc.want {
			t.Errorf("client asked %q, server answered %q, want %q", tc.asked, got, tc.want)
		}
	}
	if got := negotiateVersion(nil); got != protocolVersion {
		t.Errorf("a handshake with no version got %q, want %q", got, protocolVersion)
	}
}
