// Package when resolves the time expressions people put in questions.
//
// "what was I working on about five weeks ago" is not a retrieval query with an
// unusual vocabulary. It is a retrieval query with a filter attached, and the
// filter is the part that decides the answer. Matching those words against
// stored text finds nothing, because nobody writes "five weeks ago" in a note —
// they write "ran the drop test series", on a day that happens to be five weeks
// back. Resolving the phrase against a clock is the only thing that connects the
// question to the record.
//
// The parser is deliberately small. It recognises a fixed set of shapes and
// returns nothing for everything else. A time filter that fires on a phrase the
// asker did not mean is worse than no filter at all, because it removes context
// silently — so the bias is heavily toward not matching. Everything it does
// match is reported to the reader with the dates it resolved to, which turns a
// wrong guess from something invisible into something correctable.
//
// What it deliberately does not do: absolute dates ("on 4 March"), open-ended
// comparisons ("before the Pegatron signing"), and anything needing a model.
// Those are real questions and this is not the place to guess at them.
package when

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// A Window is a span of time named by a phrase in a question. From and To are
// inclusive.
type Window struct {
	From, To time.Time
	// Phrase is the words that named it, as the asker wrote them. Kept so the
	// reader can be told what was assumed on their behalf.
	Phrase string
}

// Contains reports whether a unix timestamp falls inside the window.
//
// Undated material is always inside. Excluding what cannot be measured would
// mean a note with no recorded time silently disappears the moment anyone asks
// a question with a date in it, which is the wrong way for a filter to fail.
func (w Window) Contains(ts int64) bool {
	if ts == 0 {
		return true
	}
	t := time.Unix(ts, 0)
	return !t.Before(w.From) && !t.After(w.To)
}

// String renders the window as the phrase plus the dates it resolved to, so a
// reader who meant something else can see that and say so.
func (w Window) String() string {
	const layout = "2 Jan 2006"
	from, to := w.From.Format(layout), w.To.Format(layout)
	if from == to {
		return fmt.Sprintf("%q (%s)", w.Phrase, from)
	}
	return fmt.Sprintf("%q (%s – %s)", w.Phrase, from, to)
}

// Parse finds the first time expression in text and resolves it against now.
// The bool is false when there is nothing it recognises, which is the common
// case and not an error.
func Parse(text string, now time.Time) (Window, bool) {
	s := strings.ToLower(strings.Join(strings.Fields(text), " "))

	// Ordered most specific first. "in the last week" is a trailing window and
	// "last week" is the previous calendar week; the only thing separating them
	// is the article, so the shape that requires it has to be tried first.
	for _, m := range matchers {
		loc := m.re.FindStringSubmatchIndex(s)
		if loc == nil {
			continue
		}
		groups := make([]string, 0, 4)
		for i := 2; i < len(loc); i += 2 {
			if loc[i] < 0 {
				groups = append(groups, "")
				continue
			}
			groups = append(groups, s[loc[i]:loc[i+1]])
		}
		w, ok := m.resolve(groups, now)
		if !ok {
			continue
		}
		w.Phrase = strings.TrimSpace(s[loc[0]:loc[1]])
		return w, true
	}
	return Window{}, false
}

type matcher struct {
	re      *regexp.Regexp
	resolve func(groups []string, now time.Time) (Window, bool)
}

// The number words worth recognising. Beyond a dozen people write digits.
var numbers = map[string]int{
	"a": 1, "an": 1, "one": 1, "couple": 2, "couple of": 2, "two": 2, "few": 3,
	"three": 3, "four": 4, "five": 5, "six": 6, "seven": 7, "eight": 8,
	"nine": 9, "ten": 10, "eleven": 11, "twelve": 12,
}

const numWords = `\d+|a|an|one|couple of|couple|two|few|three|four|five|six|seven|eight|nine|ten|eleven|twelve`
const unitWords = `hour|day|week|fortnight|month|quarter|year`

var matchers = []matcher{
	// "about five weeks ago", "3 days ago", "a month ago"
	{
		re: regexp.MustCompile(
			`\b(about|around|roughly|approximately|some)?\s*(` + numWords + `)\s+(` + unitWords + `)s?\s+ago\b`),
		resolve: agoWindow,
	},
	// "in the last two weeks", "over the past month", "within the last 3 days"
	{
		re: regexp.MustCompile(
			`\b(?:in|over|during|within|for)?\s*the\s+(?:last|past|previous)\s+(?:(` + numWords + `)\s+)?(` + unitWords + `)s?\b`),
		resolve: trailingWindow,
	},
	// "last 3 weeks", "past two days" — the same trailing sense without the
	// article, which is unambiguous once a count is present.
	{
		re: regexp.MustCompile(
			`\b(?:last|past)\s+(` + numWords + `)\s+(` + unitWords + `)s\b`),
		resolve: trailingWindow,
	},
	// "last week", "previous month" — the calendar period before this one.
	{
		re:      regexp.MustCompile(`\b(?:last|previous)\s+(week|month|quarter|year)\b`),
		resolve: previousPeriod,
	},
	// "this week", "this month" — the current period, up to now.
	{
		re:      regexp.MustCompile(`\bthis\s+(week|month|quarter|year)\b`),
		resolve: currentPeriod,
	},
	{
		re:      regexp.MustCompile(`\b(yesterday|today)\b`),
		resolve: namedDay,
	},
}

// agoWindow centres a span on a point in the past.
//
// The width comes from the unit the asker chose, because that is what they told
// us about their own precision: someone who says "five weeks ago" is not asking
// about a particular Tuesday, and someone who says "three days ago" is. So the
// window is half the unit either side — and a vagueness marker ("about",
// "roughly") doubles it, since that word exists precisely to say the number is
// approximate.
func agoWindow(g []string, now time.Time) (Window, bool) {
	n, ok := count(g[1])
	if !ok {
		return Window{}, false
	}
	span, ok := nominal(g[2])
	if !ok {
		return Window{}, false
	}
	centre := now.Add(-time.Duration(n) * span)
	half := span / 2
	if g[0] != "" {
		half = span
	}
	return Window{From: centre.Add(-half), To: centre.Add(half)}, true
}

// trailingWindow runs from a point in the past up to now. "In the last month"
// includes today; "a month ago" does not, and conflating them is the difference
// between "what have I been doing" and "what was I doing then".
func trailingWindow(g []string, now time.Time) (Window, bool) {
	n := 1
	if g[0] != "" {
		var ok bool
		if n, ok = count(g[0]); !ok {
			return Window{}, false
		}
	}
	span, ok := nominal(g[1])
	if !ok {
		return Window{}, false
	}
	return Window{From: now.Add(-time.Duration(n) * span), To: now}, true
}

// previousPeriod resolves to calendar boundaries rather than a rolling span.
// "Last month" said on the 2nd means the whole of the month before, not the
// thirty days ending on the 2nd — those are different sets of days and only one
// of them is what anybody means.
func previousPeriod(g []string, now time.Time) (Window, bool) {
	start, ok := periodStart(g[0], now)
	if !ok {
		return Window{}, false
	}
	prev, ok := periodStart(g[0], start.Add(-time.Second))
	if !ok {
		return Window{}, false
	}
	return Window{From: prev, To: start.Add(-time.Second)}, true
}

func currentPeriod(g []string, now time.Time) (Window, bool) {
	start, ok := periodStart(g[0], now)
	if !ok {
		return Window{}, false
	}
	return Window{From: start, To: now}, true
}

func namedDay(g []string, now time.Time) (Window, bool) {
	start := startOfDay(now)
	if g[0] == "yesterday" {
		start = start.AddDate(0, 0, -1)
	}
	return Window{From: start, To: start.AddDate(0, 0, 1).Add(-time.Second)}, true
}

func periodStart(unit string, t time.Time) (time.Time, bool) {
	d := startOfDay(t)
	switch unit {
	case "week":
		// Monday. Go numbers Sunday zero, which would put the boundary in the
		// middle of a working week.
		offset := (int(d.Weekday()) + 6) % 7
		return d.AddDate(0, 0, -offset), true
	case "month":
		return time.Date(d.Year(), d.Month(), 1, 0, 0, 0, 0, d.Location()), true
	case "quarter":
		q := time.Month((int(d.Month())-1)/3*3 + 1)
		return time.Date(d.Year(), q, 1, 0, 0, 0, 0, d.Location()), true
	case "year":
		return time.Date(d.Year(), time.January, 1, 0, 0, 0, 0, d.Location()), true
	}
	return time.Time{}, false
}

func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func count(s string) (int, bool) {
	if s == "" {
		return 1, true
	}
	if n, ok := numbers[s]; ok {
		return n, true
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 || n > 1000 {
		return 0, false
	}
	return n, true
}

// nominal is a unit's length for span arithmetic. Months and years are the
// averages rather than calendar-exact, which is right here: these lengths are
// only ever used to place an approximate window, and a caller wanting calendar
// precision is asking for a period, not a span.
func nominal(unit string) (time.Duration, bool) {
	switch unit {
	case "hour":
		return time.Hour, true
	case "day":
		return 24 * time.Hour, true
	case "week":
		return 7 * 24 * time.Hour, true
	case "fortnight":
		return 14 * 24 * time.Hour, true
	case "month":
		return 30 * 24 * time.Hour, true
	case "quarter":
		return 91 * 24 * time.Hour, true
	case "year":
		return 365 * 24 * time.Hour, true
	}
	return 0, false
}
