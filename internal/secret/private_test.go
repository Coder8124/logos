package secret

import (
	"strings"
	"testing"
)

func TestAPrivateSpanIsKeptOutAndCounted(t *testing.T) {
	for in, want := range map[string]string{
		"deploy with <private>the staging password is hunter2</private> then test": "deploy with [private] then test",
		"two <PRIVATE>a</PRIVATE> and <private x=1>b\nc</private> spans":           "two [private] and [private] spans",
		// A hook keeps a prompt cut to 160 characters, so the closing tag may be
		// gone; what follows an opened span is the span's until shown otherwise.
		"fix it <private>my home address is 12 Elm": "fix it [private]",
	} {
		got, found := StripPrivate("note", in)
		if got != want {
			t.Errorf("StripPrivate(%q) = %q, want %q", in, got, want)
		}
		if len(found) != strings.Count(want, PrivateMarker) {
			t.Errorf("StripPrivate(%q) counted %d, want %d", in, len(found), strings.Count(want, PrivateMarker))
		}
	}
}

// Every write that masks secrets keeps private spans out too, and the receipt
// says how many — a span that vanished unannounced reads as lost text.
func TestMaskKeepsOutPrivateSpansAndTheReceiptSaysSo(t *testing.T) {
	got, found := Mask("note", "the fix <private>Alice's phone 555-0100</private> works")
	if strings.Contains(got, "555-0100") {
		t.Fatalf("Mask kept the private span: %q", got)
	}
	if s := Summary(found); !strings.Contains(s, "Kept out 1 <private> span") || strings.Contains(s, "secret-shaped") {
		t.Errorf("Summary = %q, want it to name the private span and no secret", s)
	}
	// Masking text that was already masked finds nothing new: a checkpoint
	// masks its folded notes a second time.
	if _, again := Mask("note", got); len(again) != 0 {
		t.Errorf("re-masking found %+v in %q", again, got)
	}
}

// The lazy match stopped at the first closing tag, so a span inside a span
// closed the outer one early and what the person put after it was kept.
func TestANestedPrivateSpanKeepsOutTheWholeOuterSpan(t *testing.T) {
	out, found := StripPrivate("", "a <private>x <PRIVATE>y</private> leak</private> b")
	if strings.Contains(out, "leak") || strings.Contains(out, "</private>") || out != "a [private] b" || len(found) != 1 {
		t.Errorf("got %q with %d span(s), want %q with 1", out, len(found), "a [private] b")
	}
}

// `\b` matched before a hyphen and a slash, so <private-notes> and <private/>
// opened a span that nothing closed, and the rest of the turn went with it —
// counted and announced as a span the person marked when they had marked none.
func TestATagThatOnlyLooksLikePrivateOpensNoSpan(t *testing.T) {
	for _, s := range []string{
		"use <private/> tags then deploy",
		"<private-notes>z</private-notes> keep",
		"see privateSpan in private.go",
	} {
		if out, found := StripPrivate("", s); out != s || len(found) != 0 {
			t.Errorf("StripPrivate(%q) = %q with %d span(s), want it untouched", s, out, len(found))
		}
	}
	if out, _ := StripPrivate("", `<private reason="pin">4417</private> ok`); out != "[private] ok" {
		t.Errorf("a span with an attribute was not kept out: %q", out)
	}
}
