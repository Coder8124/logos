package session

import "testing"

// A tester's agent asked for "Saathi backend" with the work filed as saathi,
// and was told to report that nothing was recorded.
func TestAProjectNameInsideALongerNameIsFoundAndNothingLooserIs(t *testing.T) {
	known := []string{"saathi", "kestrel", "shop", "shop/fix-auth"}
	for _, tc := range []struct{ given, want string }{
		{"Saathi backend", "saathi"},
		{"saathi-backend", "saathi"},
		{"the kestrel quote", "kestrel"},
		{"saath backend", ""},       // a fragment of a name is a typo, not a match
		{"kestral", ""},             // a typo gets the list, not a guess
		{"saathi and kestrel", ""},  // two projects named: no coin flip
		{"shop fix auth tests", ""}, // shop and shop/fix-auth both inside
		{"", ""},
	} {
		if got := NameInside(tc.given, known); got != tc.want {
			t.Errorf("NameInside(%q) = %q, want %q", tc.given, got, tc.want)
		}
	}
}
