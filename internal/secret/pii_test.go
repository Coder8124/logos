package secret

import (
	"os"
	"strings"
	"testing"
)

func TestAnEmailAddressIsMasked(t *testing.T) {
	for _, in := range []string{
		"Contact jane.doe@acme-corp.com about it",
		"Author: Jane Doe <jane+logos@mail.example.co.uk>",
		`{"owner":"j.doe@example.org"}`,
	} {
		for name, mask := range map[string]func(string, string) (string, []Redaction){"Mask": Mask, "MaskAll": MaskAll} {
			out, found := mask("t", in)
			if strings.Contains(out, "@") || len(found) == 0 || found[0].Reason != "email address" {
				t.Errorf("%s left an address in %q: %q %v", name, in, out, found)
			}
		}
	}
	// What looks like an address and is not one.
	for _, in := range []string{"git@github.com:Coder8124/logos.git", "git clone https://token@host.example/repo", "lodash@4.17.21", "typescript@5.6.3 and @types/node@22.9.0"} {
		if out, _ := Mask("t", in); out != in {
			t.Errorf("masked a non-address %q: %q", in, out)
		}
	}
}

func TestAPhoneNumberIsMaskedButADateAndABuildNumberAreNot(t *testing.T) {
	for _, in := range []string{
		"call (415) 555-0132 now",
		"call 415-555-0132 now",
		"whatsapp +44 20 7946 0958",
		"tel: +1 415 555 0132.",
	} {
		out, found := Mask("t", in)
		if len(found) != 1 || found[0].Reason != "phone number" || strings.ContainsAny(out, "0123456789") && !strings.Contains(out, Marker) {
			t.Errorf("phone survived in %q: %q %v", in, out, found)
		}
	}
	for _, in := range []string{"2026-11-22 build 20261122.4", "+2024-11-22 in the diff", "ip 192.168.100.234", "v1.2.3 +1.2.3", "pid 48213 at 12:34:56.789"} {
		if out, _ := Mask("t", in); out != in {
			t.Errorf("masked a non-phone %q: %q", in, out)
		}
	}
}

func TestCookieAndKeyHeaderValuesAreMasked(t *testing.T) {
	for _, c := range []struct{ in, keep, gone string }{
		{`curl -H "Cookie: session=9f8a7b6c5d4e3f2a1b; theme=dark" https://x.test/`, "https://x.test/", "9f8a7b6c5d4e3f2a1b"},
		{"Set-Cookie: sid=abc123def456; HttpOnly", "Set-Cookie:", "abc123def456"},
		{"X-API-Key: 7d9f2c4a8b1e6035", "X-API-Key:", "7d9f2c4a8b1e6035"},
		{`curl -H 'x-auth-token: tok_8f7e6d5c4b3a' -s`, "-s", "tok_8f7e6d5c4b3a"},
		{"Proxy-Authorization: Basic dXNlcjpwYXNzd29yZA==", "Proxy-Authorization:", "dXNlcjpwYXNzd29yZA"},
	} {
		for name, mask := range map[string]func(string, string) (string, []Redaction){"Mask": Mask, "MaskAll": MaskAll} {
			out, found := mask("t", c.in)
			if strings.Contains(out, c.gone) || !strings.Contains(out, c.keep) || len(found) == 0 {
				t.Errorf("%s: header value survived or text was lost in %q: %q", name, c.in, out)
			}
		}
	}
	// Prose that names a header is not a header.
	for _, in := range []string{"Cookie: sessions are httpOnly and signed", "X-API-Key: required"} {
		if out, _ := Mask("t", in); out != in {
			t.Errorf("masked prose %q: %q", in, out)
		}
	}
}

func TestMaskingPersonalDataIsIdempotent(t *testing.T) {
	in := "mail jane@acme-corp.com or +44 20 7946 0958, Cookie: sid=abc123def456"
	once, _ := Mask("t", in)
	twice, found := Mask("t", once)
	if once != twice || len(found) != 0 {
		t.Errorf("a second pass changed %q to %q (%v)", once, twice, found)
	}
}

// TestEveryRuleFileIsHashedIntoRules: a rules file left out of the hash would
// change what is masked without making any archived copy eligible for a new
// pass, the silent failure Rules exists to prevent.
func TestEveryRuleFileIsHashedIntoRules(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		n := e.Name()
		if !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") || n == "rules.go" {
			continue
		}
		if _, ok := ruleSources[n]; !ok {
			t.Errorf("%s is not in ruleSources, so editing it would not change Rules", n)
		}
	}
	base := hashSources(ruleSources)
	for name := range ruleSources {
		changed := map[string][]byte{}
		for k, v := range ruleSources {
			changed[k] = v
		}
		changed[name] = append(append([]byte{}, changed[name]...), '\n')
		if hashSources(changed) == base {
			t.Errorf("editing %s does not change Rules", name)
		}
	}
	if Rules != base || len(gitleaksSrc) == 0 || len(piiSrc) == 0 {
		t.Errorf("Rules does not cover the embedded sources")
	}
}

func TestGitAtACompanyAddressInProseIsMaskedButAGitRemoteIsNot(t *testing.T) {
	if out, _ := Mask("t", "mail git@company.com about it"); strings.Contains(out, "git@company.com") {
		t.Errorf("an address was exempted for its user name: %q", out)
	}
	for _, in := range []string{"git@github.com:Coder8124/logos.git", "ssh://git@github.com/Coder8124/logos.git"} {
		if out, _ := Mask("t", in); out != in {
			t.Errorf("masked a git remote %q: %q", in, out)
		}
	}
}

func TestACookieHeaderMasksItsPairsAndNotTheProseAfterThem(t *testing.T) {
	out, _ := Mask("t", "Cookie: a=b and then lots more prose")
	if strings.Contains(out, "a=b") || !strings.Contains(out, "and then lots more prose") {
		t.Errorf("want the pair masked and the prose kept: %q", out)
	}
	out, _ = Mask("t", "Cookie: sid=abc123; theme=dark; lang=en and the rest")
	if strings.Contains(out, "theme=dark") || !strings.Contains(out, "and the rest") {
		t.Errorf("want every pair masked and the prose kept: %q", out)
	}
}
