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

// On a real machine's Cursor history every one of the 99 redactions was a
// filesystem path or a Java class name, and not one was a secret. `cd
// /Users/me/IdeaProjects/Thing && ./gradlew build` reached the vault as `cd
// [REDACTED] && ./gradlew build`, which loses the one fact saying where the
// command ran and tells a user with no secrets that secrets were found — the
// report that teaches them to ignore the report that matters.
func TestAFilesystemPathIsNotRedactedAsASecret(t *testing.T) {
	for _, path := range []string{
		"/Users/pragun/IdeaProjects/MinecraftMod",
		"/Users/pragun/PycharmProjects/stellarius",
		"./src/main/java/com/kingdomgame/model",
		"../SiblingProject/BuildOutput",
		"~/GoProjects/SomeLongDirectoryName",
	} {
		cmd := "cd " + path + " && ./gradlew build"
		if got := Redact(cmd); got != cmd {
			t.Errorf("a path was masked as a credential:\n  in:  %s\n  out: %s", cmd, got)
		}
	}
}

// The other half of the same change: relaxing the path case must not cost the
// credentials the entropy check is there for. An AWS secret access key splits
// into short word-shaped segments exactly as a relative path does, so it stays
// flagged on purpose — missing a path is cheap, missing a key is not.
func TestRelaxingThePathCaseStillMasksRealCredentials(t *testing.T) {
	for _, secret := range []string{
		"wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9",
		"ghp_abcdefghijklmnopqrstuvwxyz0123",
		"sk-live-abcdefghijklmnopqrstuvwxyz",
	} {
		line := "export TOKEN=" + secret
		got := Redact(line)
		if strings.Contains(got, secret) {
			t.Errorf("a credential survived redaction:\n  in:  %s\n  out: %s", line, got)
		}
	}
}
