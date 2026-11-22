package secret

import (
	"bufio"
	"os"
	"strings"
	"testing"
)

// corpusLines reads a testdata file of "id<TAB>text" or plain lines, skipping
// comments. A literal \n in a case stands for a newline.
func corpusLines(t *testing.T, name string) [][2]string { return readCorpus(t, name) }

func readCorpus(t testing.TB, name string) [][2]string {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out [][2]string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "# ") {
			continue
		}
		line = strings.ReplaceAll(line, `\n`, "\n")
		if name == "tokens.txt" {
			// Each sample is stored split by a "|" so that no literal in the
			// repository is shaped like a live credential: a secret scanner
			// that blocks a push over a synthetic token is a false alarm
			// that teaches people to bypass the real ones.
			line = strings.Replace(line, "|", "", 1)
		}
		if id, text, ok := strings.Cut(line, "\t"); ok && !strings.Contains(id, " ") {
			out = append(out, [2]string{id, text})
		} else {
			out = append(out, [2]string{"", line})
		}
	}
	return out
}

// TestEveryPortedRuleMasksItsOwnSampleToken also fails for a rule with no
// sample: a rule nobody has shown to fire is a rule that may never have.
func TestEveryPortedRuleMasksItsOwnSampleToken(t *testing.T) {
	samples := map[string]string{}
	for _, c := range corpusLines(t, "tokens.txt") {
		samples[c[0]] = c[1]
	}
	for _, r := range tokenRules {
		sample, ok := samples[r.id]
		if !ok {
			t.Errorf("rule %s has no sample in testdata/tokens.txt", r.id)
			continue
		}
		for _, ctx := range []string{"%s", "export X=%s", "the key is %s and then more words", `{"v":"%s"}`} {
			in := strings.Replace(ctx, "%s", sample, 1)
			for name, mask := range map[string]func(string, string) (string, []Redaction){"Mask": Mask, "MaskAll": MaskAll} {
				var (
					out   string
					found []Redaction
				)
				out, found = mask("t", in)
				if out == in || len(found) == 0 || !strings.Contains(out, Marker) {
					t.Errorf("%s: %s did not mask its sample in %q: %q", r.id, name, ctx, out)
				}
				// The rule itself, not a neighbour or the entropy check, must be
				// the one that fires on the bare sample.
				if ctx == "%s" && name == "Mask" && !reasonNamed(found, r.id) {
					t.Errorf("%s: Mask masked its sample but a different rule did: %v", r.id, found)
				}
			}
		}
	}
	for _, c := range corpusLines(t, "tokens.txt") {
		if out, _ := Mask("t", c[1]); out == c[1] {
			t.Errorf("sample %s is not masked at all: %q", c[0], c[1])
		}
	}
}

func reasonNamed(found []Redaction, id string) bool {
	for _, f := range found {
		if strings.Contains(f.Reason, "("+id+")") {
			return true
		}
	}
	return false
}

// TestAGitHashAndAUUIDAreNotMasked runs the whole ordinary corpus — a git log,
// UUIDs, base64 image data, paths, versions, prose — through both strengths.
// The cost of a false mask is a transcript whose commands no longer say what
// they ran, and a report of secrets that were never there.
func TestAGitHashAndAUUIDAreNotMasked(t *testing.T) {
	for _, c := range corpusLines(t, "ordinary.txt") {
		for name, mask := range map[string]func(string, string) (string, []Redaction){"Mask": Mask, "MaskAll": MaskAll} {
			if out, found := mask("t", c[1]); out != c[1] {
				t.Errorf("%s masked ordinary text %q -> %q (%v)", name, c[1], out, found)
			}
		}
	}
}

// TestAHighEntropyValueBesideAKeyNameIsStillMasked: a token with no known
// shape is caught by what sits next to it, and that must survive the move to a
// table of shapes.
func TestAHighEntropyValueBesideAKeyNameIsStillMasked(t *testing.T) {
	for _, in := range []string{
		"api_key = Zq8Xv3Lm0Rt7Yp2Kd9Wn4Bc6Hs1Jf5G",
		"SECRET_TOKEN=aB3xK9mQ2vL7nR4tY8wZ1pC6dF0hJ5sG",
		`{"password":"Qm7Xk2Vb9Lr4Tn8Yc3Wz6Hd1Jf5Ps0Ag"}`,
		"--token Hs4Yc9Vb2Lr7Tn3Xk8Wz1Qm6Jd0Pf5A",
	} {
		out, found := MaskAll("t", in)
		if out == in || len(found) == 0 {
			t.Errorf("an unshaped secret survived: %q -> %q", in, out)
		}
	}
}

// TestCorpusReport prints what each strength catches and falsely masks, so a
// change to the rules is measured rather than asserted. Run with -v.
func TestCorpusReport(t *testing.T) {
	toks := corpusLines(t, "tokens.txt")
	ord := corpusLines(t, "ordinary.txt")
	known := corpusLines(t, "known_false_masks.txt")
	for name, mask := range map[string]func(string, string) (string, []Redaction){"Mask": Mask, "MaskAll": MaskAll} {
		caught, missed := 0, []string{}
		for _, c := range toks {
			if out, _ := mask("t", c[1]); out != c[1] {
				caught++
			} else {
				missed = append(missed, c[0])
			}
		}
		falseMasks := 0
		for _, c := range ord {
			if out, _ := mask("t", c[1]); out != c[1] {
				falseMasks++
				t.Logf("  %s false mask: %q", name, c[1])
			}
		}
		knownMasked := 0
		for _, c := range known {
			if out, _ := mask("t", c[1]); out != c[1] {
				knownMasked++
			}
		}
		t.Logf("REPORT %s known pre-existing false masks (known_false_masks.txt): %d/%d", name, knownMasked, len(known))
		t.Logf("REPORT %s: caught %d/%d, false masks %d/%d, missed %v", name, caught, len(toks), falseMasks, len(ord), missed)
	}
}

func sampleFor(t *testing.T, id string) string {
	t.Helper()
	for _, c := range corpusLines(t, "tokens.txt") {
		if c[0] == id {
			return c[1]
		}
	}
	t.Fatalf("no sample for %s", id)
	return ""
}

// The Azure rule used to consume the delimiter after its secret, which was the
// delimiter the next secret needed before it.
func TestTwoSpaceSeparatedAzureSecretsAreBothMasked(t *testing.T) {
	a := sampleFor(t, "azure-ad-client-secret")
	b := "Xyz" + a[3:]
	for name, mask := range map[string]func(string, string) (string, []Redaction){"Mask": Mask, "MaskAll": MaskAll} {
		out, _ := mask("t", a+" "+b)
		if strings.Contains(out, a[3:]) || strings.Count(out, Marker) != 2 {
			t.Errorf("%s: want both secrets masked, got %q", name, out)
		}
	}
}

func TestAMixedCaseAlibabaKeyIsMasked(t *testing.T) {
	in := "ALIBABA_KEY=LTAI5tB5yQ8kZp3NxRw7Lm2c"
	if out, _ := Mask("t", in); strings.Contains(out, "LTAI5tB5") {
		t.Errorf("mixed-case Alibaba key survived: %q", out)
	}
}

func TestAHuggingFaceOrgTokenWithCapitalsIsMasked(t *testing.T) {
	in := "HF_ORG=api_org_" + "AbCdEfGhIjKlMnOpQrStUvWxYzAbCdEfGh"
	if out, _ := Mask("t", in); strings.Contains(out, "AbCdEfGh") {
		t.Errorf("org token with capitals survived: %q", out)
	}
}

// Fly's pattern once accepted fm1_ and fm2r_, which its keywords could never
// wake; Gitleaks' shapes are fm1a_, fm1r_ and fm2_ only.
func TestOnlyTheFlyTokenPrefixesFlyUsesAreMasked(t *testing.T) {
	body := strings.Repeat("Ab3dEf6hIj", 11)
	for _, p := range []string{"fm1a_", "fm1r_", "fm2_"} {
		if out, _ := Mask("t", "FLY="+p+body); strings.Contains(out, body) {
			t.Errorf("%s token survived: %q", p, out)
		}
	}
	for _, p := range []string{"fm1_", "fm2r_", "fm3_"} {
		if out, found := Mask("t", "x "+p+body); reasonNamed(found, "fly-access-token") {
			t.Errorf("%s matched the fly rule: %q", p, out)
		}
	}
}
