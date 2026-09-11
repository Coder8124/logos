package ingest

import (
	"strings"
	"testing"
)

func TestAnOpenAIStyleKeyIsRedactedByPrefix(t *testing.T) {
	masked, found := redactText("Commands", "curl -H 'x-api-key: sk-proj-abcdefghijklmnopqrstuvwxyz' https://api.example.com")
	if contains(masked, "sk-proj-abcdefghijklmnopqrstuvwxyz") {
		t.Fatalf("secret survived redaction: %q", masked)
	}
	if len(found) != 1 || found[0].Reason == "" {
		t.Fatalf("expected one redaction reported, got %+v", found)
	}
}

func TestAGitHubTokenIsRedactedByPrefix(t *testing.T) {
	masked, found := redactText("Verified", "pushed with ghp_16C7e42F292c6912E7710c838347Ae178B4a")
	if contains(masked, "ghp_16C7e42F292c6912E7710c838347Ae178B4a") {
		t.Fatalf("secret survived redaction: %q", masked)
	}
	if len(found) != 1 {
		t.Fatalf("expected one redaction, got %+v", found)
	}
}

func TestAnAWSAccessKeyIsRedactedByPrefix(t *testing.T) {
	masked, found := redactText("Commands", "export AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE")
	if contains(masked, "AKIAIOSFODNN7EXAMPLE") {
		t.Fatalf("secret survived redaction: %q", masked)
	}
	if len(found) != 1 {
		t.Fatalf("expected one redaction, got %+v", found)
	}
}

func TestAnAuthorizationHeaderLineIsRedactedWhole(t *testing.T) {
	masked, found := redactText("Commands", "Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U")
	if contains(masked, "eyJhbGciOiJIUzI1NiJ9") {
		t.Fatalf("Authorization payload survived redaction: %q", masked)
	}
	if len(found) != 1 || found[0].Reason != "Authorization header" {
		t.Fatalf("expected one Authorization-header redaction, got %+v", found)
	}
}

func TestAHighEntropyTokenWithNoKnownPrefixIsStillCaught(t *testing.T) {
	masked, found := redactText("Commands", "token=zQ8pR2vL9xM4wK7nT1cB6yH3sF0jD5gA")
	if contains(masked, "zQ8pR2vL9xM4wK7nT1cB6yH3sF0jD5gA") {
		t.Fatalf("high-entropy secret survived redaction: %q", masked)
	}
	if len(found) != 1 || found[0].Reason != "high-entropy token" {
		t.Fatalf("expected one high-entropy redaction, got %+v", found)
	}
}

// The redaction must not be so eager that ordinary command output — file
// paths, git commit hashes, UUIDs — gets mangled. A review queue full of
// false-positive [REDACTED] markers is its own failure: reviewers stop
// trusting the tool.
func TestOrdinaryPathsHashesAndUUIDsAreLeftAlone(t *testing.T) {
	cases := []string{
		"go build ./internal/ingest/...",
		"git commit -am 'fix the thing'",
		"commit 4f2b8c1e9a7d3f6b5c0a1e2d3f4b5c6a7d8e9f0a",
		"session 3fa85f64-5717-4562-b3fc-2c963f66afa6 restored",
		"internal/mcpserver/server.go:362",
	}
	for _, in := range cases {
		masked, found := redactText("Commands", in)
		if masked != in || len(found) != 0 {
			t.Errorf("ordinary text was redacted: %q -> %q (%+v)", in, masked, found)
		}
	}
}

func TestRedactCandidateTextCoversEveryFreeTextField(t *testing.T) {
	c := Candidate{
		Commands: []string{"curl -H 'Authorization: Bearer sk-abcdefghijklmno1234567890'"},
		Verified: []string{"login works with ghp_16C7e42F292c6912E7710c838347Ae178B4a"},
		Failed:   []string{"AKIAIOSFODNN7EXAMPLE rejected"},
		Blockers: []string{"waiting on sk-live-abcdefghijklmnopqrstuvwx"},
		Next:     "rotate sk-live-abcdefghijklmnopqrstuvwxyzAB",
	}
	found := redactCandidateText(&c)
	if len(found) == 0 {
		t.Fatal("expected redactions across every field, got none")
	}
	raw := c.Markdown()
	for _, secret := range []string{
		"sk-abcdefghijklmno1234567890",
		"ghp_16C7e42F292c6912E7710c838347Ae178B4a",
		"AKIAIOSFODNN7EXAMPLE",
		"sk-live-abcdefghijklmnopqrstuvwx",
		"sk-live-abcdefghijklmnopqrstuvwxyzAB",
	} {
		if contains(raw, secret) {
			t.Errorf("candidate markdown still contains a raw secret: %q\n%s", secret, raw)
		}
	}
}

func contains(s, sub string) bool {
	return strings.Contains(s, sub)
}
