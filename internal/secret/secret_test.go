package secret

import (
	"strings"
	"testing"
)

func TestEveryAddedCredentialShapeIsMaskedInAnAgentsOwnText(t *testing.T) {
	for name, key := range map[string]string{
		"Google API key":     "AIzaSyA1b2C3d4E5f6G7h8I9j0K1l2M3n4O5p6Q",
		"Hugging Face token": "hf_Qz8Rt5Uv2Wx9Ya6Bc3De0Fg7Hi4Jk1Lm8No5Pq",
		"Groq key":           "gsk_Ab3De6Gh9Jk2Mn5Pq8St1Vw4",
		"npm token":          "npm_Ab3De6Gh9Jk2Mn5Pq8St1Vw4Yz7Bc0Ef3Gh6",
		"Mailgun key":        "key-0123456789abcdefABCDEF0123456789",
		"JSON Web Token":     "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U",
		"Shopify token":      "shpat_0123456789abcdef0123456789abcdef",
	} {
		for _, s := range []string{
			"the key is " + key + ".",
			"export TOKEN=" + key,
			`{"token":"` + key + `"}`,
		} {
			got, found := Mask("Verified", s)
			if strings.Contains(got, key) || len(found) == 0 {
				t.Errorf("%s survived in %q: %q", name, s, got)
			}
		}
	}
}

func TestAPrivateKeyIsMaskedBodyAndAll(t *testing.T) {
	body := "MIIEowIBAAKCAQEAu1SU1LfVLPHCozMxH2Mo4lgOEePzNm0tRgeLezV6ffAt0gun"
	s := "pasted it:\n-----BEGIN RSA PRIVATE KEY-----\n" + body + "\n-----END RSA PRIVATE KEY-----\nthen it worked"
	got, found := Mask("State", s)
	if strings.Contains(got, body) || len(found) != 1 {
		t.Fatalf("the key body survived or was miscounted (%d): %q", len(found), got)
	}
	if !strings.Contains(got, "then it worked") {
		t.Fatalf("masked past the end of the key: %q", got)
	}
}

func TestSecretsThatSpanASeparatorAreMasked(t *testing.T) {
	for name, s := range map[string]string{
		"Slack webhook":  "posted to https://hooks.slack.com/services/T0ABCDEFG/B0ABCDEFG/abcdefghij0123456789ABCD",
		"Telegram token": "bot 123456789:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsawQ is live",
		"AWS secret key": "aws_secret_access_key = wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	} {
		got, found := Mask("Commands", s)
		if len(found) == 0 || !strings.Contains(got, Marker) {
			t.Errorf("%s survived: %q", name, got)
		}
	}
}

// Each of these starts the way a credential does and is not one. A bare
// prefix match would mask all of them, and a report of secrets in text that
// has none is what teaches a user to stop reading the report.
func TestWordsThatOnlyStartLikeACredentialAreKept(t *testing.T) {
	for _, s := range []string{
		"npm_config_cache is set in CI",
		"hf_hub_download fetches the weights",
		"key-value store",
		"dapi is the Databricks prefix",
		"go test ./internal/session -run TestWorkingNotesSurviveDeletingTheIndex",
		"github.com/Coder8124/logos/internal/session",
		"commit 3f9a1c0e8b7d6a5f4e3d2c1b0a9f8e7d6c5b4a39",
	} {
		if got, found := Mask("Verified", s); got != s || len(found) != 0 {
			t.Errorf("masked a non-secret: %q -> %q (%+v)", s, got, found)
		}
	}
}

// The entropy rule is for transcripts only; an agent's own words are held to
// known shapes. This is the line between the two.
func TestOnlyMaskAllFlagsAnUnshapedRandomToken(t *testing.T) {
	s := "the value was Xk9mQ2vR7tL4pZ8wN3bH6jF1"
	if got, _ := Mask("", s); got != s {
		t.Errorf("Mask masked a token with no credential shape: %q", got)
	}
	if got, _ := MaskAll("", s); got == s {
		t.Errorf("MaskAll kept a high-entropy token: %q", got)
	}
}
