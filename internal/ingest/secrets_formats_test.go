package ingest

import (
	"strings"
	"testing"
)

// Each of these was written to the vault in plaintext while the review
// receipt said "0 redacted", which reads as "nothing to check". Redaction
// only looked at a whole word: a known prefix had to start it and the entropy
// check rejected any quote, colon, @ or !, so a key behind --token=, inside
// JSON, in a URL's password or after mysql's -p all rode through.
func TestCommonCredentialFormatsAreRedacted(t *testing.T) {
	for _, tc := range []struct{ in, secret string }{
		{"gh auth login --token=ghp_abcdefghijklmnopqrstuvwxyz0123", "ghp_abcdefghijklmnopqrstuvwxyz0123"},
		{`{"api_key":"sk-live-Zx8Yw7Vu6Ts5Rq4Po3"}`, "sk-live-Zx8Yw7Vu6Ts5Rq4Po3"},
		{"psql postgres://admin:hunter2hunter2@db.internal:5432/prod", "hunter2hunter2"},
		{"git push https://oauth2:glpat-xxxxYYYYzzzz1234@gitlab.com/x.git", "glpat-xxxxYYYYzzzz1234"},
		{"mysql -u root -pS3cretPassw0rd!", "S3cretPassw0rd"},
		{`curl -H "Authorization: Bearer abc" x`, "abc"},
		{"export GITLAB_TOKEN=glpat-xxxxYYYYzzzz1234", "glpat-xxxxYYYYzzzz1234"},
	} {
		masked, found := redactText("Commands", tc.in)
		if strings.Contains(masked, tc.secret) {
			t.Errorf("secret survived redaction: %q -> %q", tc.in, masked)
		}
		if len(found) == 0 {
			t.Errorf("a redaction in %q was not reported", tc.in)
		}
	}
}

// Looking inside words, URLs and JSON must not start masking the ordinary
// text around a credential: the command, the host, the keys.
func TestRedactingInsideAWordKeepsTheTextAroundTheSecret(t *testing.T) {
	for in, want := range map[string]string{
		"psql postgres://admin:hunter2hunter2@db.internal:5432/prod": "psql postgres://admin:[REDACTED]@db.internal:5432/prod",
		`{"api_key":"sk-live-Zx8Yw7Vu6Ts5Rq4Po3"}`:                   `{"api_key":"[REDACTED]"}`,
		`curl -H "Authorization: Bearer abc" x`:                      `curl -H "Authorization: [REDACTED]" x`,
		"mysql -u root -pS3cretPassw0rd!":                            "mysql -u root -p[REDACTED]",
	} {
		if got, _ := redactText("Commands", in); got != want {
			t.Errorf("redactText(%q)\n got %q\nwant %q", in, got, want)
		}
	}
}

func TestLinksTimesAndFlagsAreLeftAlone(t *testing.T) {
	for _, in := range []string{
		"gh pr view https://github.com/Coder8124/logos/pull/1234",
		"started at 12:30:45 on 2026-09-14",
		"find . -name '*.go' -print -prune",
		"ssh git@github.com:Coder8124/logos.git",
		"key=value name:Kestrel",
	} {
		if masked, found := redactText("Commands", in); masked != in || len(found) != 0 {
			t.Errorf("ordinary text was redacted: %q -> %q (%+v)", in, masked, found)
		}
	}
}

// Masking replaced the first occurrence of the secret's text in the word, so
// when the same text also sat inside an earlier, harmless part, that part was
// masked and the credential itself was left in the vault.
func TestTheSecretItselfIsMaskedWhenItsTextAlsoAppearsEarlierInTheWord(t *testing.T) {
	in := "prefix-sk-live-abcdefghijkl=sk-live-abcdefghijkl"
	masked, _ := redactText("Commands", in)
	if want := "prefix-sk-live-abcdefghijkl=[REDACTED]"; masked != want {
		t.Errorf("got %q, want %q", masked, want)
	}
}
