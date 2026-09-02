package when

import (
	"testing"
	"time"
)

// The clock every case reasons against: a Wednesday, mid-month, mid-quarter, so
// that week, month and quarter boundaries are all distinguishable from each
// other and from "now".
var now = time.Date(2026, time.August, 12, 14, 30, 0, 0, time.UTC)

func days(n int) time.Time { return now.AddDate(0, 0, -n) }

func TestResolvesWindows(t *testing.T) {
	cases := []struct {
		text string
		// in and out are timestamps that must and must not fall inside.
		in, out []time.Time
	}{
		{
			text: "what was I working on about five weeks ago?",
			in:   []time.Time{days(35), days(30), days(41)},
			out:  []time.Time{days(5), days(45), now},
		},
		{
			// No vagueness marker, so the window is half a unit either side —
			// tight enough to mean a particular week.
			text: "what happened five weeks ago",
			in:   []time.Time{days(35), days(33)},
			out:  []time.Time{days(28), days(42)},
		},
		{
			text: "what did I do yesterday",
			in:   []time.Time{days(1)},
			out:  []time.Time{now, days(2)},
		},
		{
			text: "what have I touched in the last two weeks",
			in:   []time.Time{now, days(3), days(13)},
			out:  []time.Time{days(20)},
		},
		{
			// A trailing window includes today; a point in the past does not.
			// The difference is the whole reason both shapes exist.
			text: "over the past month",
			in:   []time.Time{now, days(29)},
			out:  []time.Time{days(40)},
		},
		{
			// July, in full — not the thirty days ending on the 12th.
			text: "what did we decide last month",
			in: []time.Time{
				time.Date(2026, time.July, 1, 0, 30, 0, 0, time.UTC),
				time.Date(2026, time.July, 31, 23, 0, 0, 0, time.UTC),
			},
			out: []time.Time{
				time.Date(2026, time.June, 30, 12, 0, 0, 0, time.UTC),
				time.Date(2026, time.August, 1, 0, 0, 1, 0, time.UTC),
			},
		},
		{
			// Monday 3 August through Sunday 9 August.
			text: "the bug I filed last week",
			in: []time.Time{
				time.Date(2026, time.August, 3, 9, 0, 0, 0, time.UTC),
				time.Date(2026, time.August, 9, 23, 0, 0, 0, time.UTC),
			},
			out: []time.Time{
				time.Date(2026, time.August, 10, 9, 0, 0, 0, time.UTC),
				time.Date(2026, time.August, 2, 9, 0, 0, 0, time.UTC),
			},
		},
		{
			text: "everything from this week",
			in:   []time.Time{time.Date(2026, time.August, 10, 9, 0, 0, 0, time.UTC), now},
			out:  []time.Time{time.Date(2026, time.August, 9, 9, 0, 0, 0, time.UTC)},
		},
		{
			text: "the migration from a year ago",
			in:   []time.Time{now.AddDate(-1, 0, 0)},
			out:  []time.Time{now},
		},
	}

	for _, c := range cases {
		w, ok := Parse(c.text, now)
		if !ok {
			t.Errorf("%q: no window parsed", c.text)
			continue
		}
		for _, ts := range c.in {
			if !w.Contains(ts.Unix()) {
				t.Errorf("%q → %s: excludes %s, which is inside", c.text, w, ts.Format(time.RFC3339))
			}
		}
		for _, ts := range c.out {
			if w.Contains(ts.Unix()) {
				t.Errorf("%q → %s: includes %s, which is outside", c.text, w, ts.Format(time.RFC3339))
			}
		}
	}
}

// The property that matters more than any single window: a filter that fires on
// a phrase nobody meant temporally removes context silently, and silent removal
// is the failure this parser exists to avoid. So it says no by default.
func TestDeclinesEverythingElse(t *testing.T) {
	for _, text := range []string{
		"did we lock the industrial design before or after signing with Pegatron?",
		"keep cutting the BOM toward target",
		"what is the last item on the list",
		"revert the last three commits",
		"continue the OTA assessment",
		"what was the previous decision on batteries",
		"plan the next DVT spin",
		"why is the per-unit cost higher than the parts add up to?",
		"",
	} {
		if w, ok := Parse(text, now); ok {
			t.Errorf("%q parsed a window it should not have: %s", text, w)
		}
	}
}

// Undated material must survive a window it cannot be measured against.
func TestUndatedIsAlwaysInside(t *testing.T) {
	w, ok := Parse("what was I doing five weeks ago", now)
	if !ok {
		t.Fatal("no window parsed")
	}
	if !w.Contains(0) {
		t.Error("a zero timestamp was excluded; undated material would vanish")
	}
}

// The window is only defensible if the reader is told what was assumed.
func TestStringNamesThePhraseAndTheDates(t *testing.T) {
	w, ok := Parse("what was I working on about five weeks ago?", now)
	if !ok {
		t.Fatal("no window parsed")
	}
	got := w.String()
	for _, want := range []string{"about five weeks ago", "Jul", "2026"} {
		if !contains(got, want) {
			t.Errorf("%s does not mention %q", got, want)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
