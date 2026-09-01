package secretary

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Coder8124/brain/internal/capture"
	"github.com/Coder8124/brain/internal/event"
	"github.com/Coder8124/brain/internal/routine"
)

// The Weekly Review — the executive-assistant briefing, delivered every Sunday.
// Where the daily Brief says "what now?", this says "here's your week": what you
// got done, what is still open, who you dealt with, what is due, where your time
// went, the habits underneath it, and what to do about all of it.
//
// Like the Brief, it is arithmetic over data already captured — commits, focus
// time, calendar, the loops you closed and left open — so it is instant, offline,
// and honest. A model may narrate a sentence on top, but every number under it
// was computed here, not guessed (the compute-then-narrate discipline that keeps
// the review trustworthy).

// WeeklyReview is the assembled week.
type WeeklyReview struct {
	Start, End      time.Time    `json:"-"`
	StartUnix       int64        `json:"start"`
	EndUnix         int64        `json:"end"`
	Accomplished    []ReviewItem `json:"accomplished"`
	Unfinished      []ReviewItem `json:"unfinished"`
	People          []PersonStat `json:"people"`
	Deadlines       []Deadline   `json:"deadlines"`
	Topics          []TopicStat  `json:"topics"`
	Habits          []string     `json:"habits"`
	Recommendations []string     `json:"recommendations"`
	Stats           WeekStats    `json:"stats"`
}

// ReviewItem is one accomplishment or open loop.
type ReviewItem struct {
	Text   string `json:"text"`
	Detail string `json:"detail"`
	TS     int64  `json:"ts"`
}

// PersonStat is someone who came up this week and how often.
type PersonStat struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
	Via   string `json:"via"` // "loop", "meeting", "note"
}

// Deadline is something due, this week or next.
type Deadline struct {
	Text string `json:"text"`
	When string `json:"when"`
	TS   int64  `json:"ts"` // 0 for fuzzy ("friday")
}

// TopicStat is where time or attention went.
type TopicStat struct {
	Label  string  `json:"label"`
	Hours  float64 `json:"hours"`
	Detail string  `json:"detail"`
}

// WeekStats are the top-line numbers.
type WeekStats struct {
	ActiveHours  float64 `json:"active_hours"`
	Meetings     int     `json:"meetings"`
	Commits      int     `json:"commits"`
	NotesCreated int     `json:"notes_created"`
	LoopsClosed  int     `json:"loops_closed"`
	LoopsOpen    int     `json:"loops_open"`
}

// Review builds the week ending at `now` (the trailing 7 days).
func Review(db *sql.DB, now time.Time) (WeeklyReview, error) {
	start := now.AddDate(0, 0, -7)
	r := WeeklyReview{Start: start, End: now, StartUnix: start.Unix(), EndUnix: now.Unix()}

	events, err := capture.Range(db, start.Unix(), now.Unix())
	if err != nil {
		return r, err
	}

	people := map[string]*PersonStat{}
	note := func(name, via string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		if p := people[strings.ToLower(name)]; p != nil {
			p.Count++
		} else {
			people[strings.ToLower(name)] = &PersonStat{Name: name, Count: 1, Via: via}
		}
	}

	// --- accomplished: closed loops, git commits, notes created ---
	if closed, err := ResolvedSince(db, start.Unix(), now.Unix()); err == nil {
		r.Stats.LoopsClosed = len(closed)
		for _, c := range closed {
			r.Accomplished = append(r.Accomplished, ReviewItem{
				Text: c.Text, Detail: "closed a loop", TS: c.ResolvedAt,
			})
			note(c.Who, "loop")
		}
	}
	for _, e := range events {
		switch e.Kind {
		case event.Commit:
			r.Stats.Commits++
			r.Accomplished = append(r.Accomplished, ReviewItem{
				Text: firstLine(e.Title), Detail: "commit in " + repoName(e), TS: e.TS,
			})
		}
	}
	// Notes created this week (first_seen in window).
	if names, err := notesCreatedBetween(db, start.Unix(), now.Unix()); err == nil {
		r.Stats.NotesCreated = len(names)
		for _, n := range names {
			r.Accomplished = append(r.Accomplished, ReviewItem{Text: n.title, Detail: "new " + n.kind + " note", TS: n.ts})
			if n.kind == "person" {
				note(n.title, "note")
			}
		}
	}
	sort.Slice(r.Accomplished, func(i, j int) bool { return r.Accomplished[i].TS > r.Accomplished[j].TS })

	// --- unfinished: open loops, stalest first ---
	if open, err := Open_(db); err == nil {
		r.Stats.LoopsOpen = len(open)
		sort.Slice(open, func(i, j int) bool { return open[i].Created < open[j].Created })
		for _, c := range open {
			age := int(now.Sub(time.Unix(c.Created, 0)).Hours() / 24)
			detail := fmt.Sprintf("open %dd", age)
			if c.Who != "" {
				detail += " · " + c.Who
			}
			r.Unfinished = append(r.Unfinished, ReviewItem{Text: c.Text, Detail: detail, TS: c.Created})
			note(c.Who, "loop")
			if c.DueHint != "" {
				r.Deadlines = append(r.Deadlines, Deadline{Text: c.Text, When: c.DueHint})
			}
		}
	}

	// --- deadlines: calendar events in the coming week ---
	if future, err := capture.Range(db, now.Unix(), now.AddDate(0, 0, 7).Unix()); err == nil {
		for _, e := range future {
			if e.Kind != event.Calendar {
				continue
			}
			r.Deadlines = append(r.Deadlines, Deadline{
				Text: e.Title, When: time.Unix(e.TS, 0).Local().Format("Mon 15:04"), TS: e.TS,
			})
		}
	}
	sort.Slice(r.Deadlines, func(i, j int) bool {
		if r.Deadlines[i].TS != r.Deadlines[j].TS {
			// dated deadlines (TS>0) before fuzzy ones (TS==0), soonest first
			if r.Deadlines[i].TS == 0 {
				return false
			}
			if r.Deadlines[j].TS == 0 {
				return true
			}
			return r.Deadlines[i].TS < r.Deadlines[j].TS
		}
		return false
	})

	// --- people talked to: meetings this week ---
	// Meeting titles are matched against known people notes rather than taken
	// verbatim, so "1:1 with Dana" credits Dana, not the whole title string.
	persons := loadPersonNames(db)
	for _, e := range events {
		if e.Kind == event.Calendar {
			r.Stats.Meetings++
			for _, name := range matchPeople(e.Title, persons) {
				note(name, "meeting")
			}
		}
	}
	for _, p := range people {
		r.People = append(r.People, *p)
	}
	sort.Slice(r.People, func(i, j int) bool { return r.People[i].Count > r.People[j].Count })
	if len(r.People) > 10 {
		r.People = r.People[:10]
	}

	// --- recurring topics: where time went (top apps + web domains) ---
	r.Topics, r.Stats.ActiveHours = topicsFrom(events)

	// --- habits: stable routines underneath the week ---
	// A long window so the baseline is real, not just this week's noise.
	if wide, err := capture.Range(db, now.AddDate(0, 0, -120).Unix(), now.Unix()); err == nil {
		for _, p := range routine.FindPeriodic(wide) {
			r.Habits = append(r.Habits, fmt.Sprintf("%s — %s on %s (%.0f%% of days)",
				p.App, p.Window(), p.Cadence(), p.Consistency*100))
			if len(r.Habits) >= 5 {
				break
			}
		}
	}

	r.Recommendations = recommend(r, now)
	return r, nil
}

// topicsFrom aggregates focus time by app and browsing by domain into the top
// few "where your attention went" rows, and returns total active hours.
func topicsFrom(events []event.Event) ([]TopicStat, float64) {
	appSecs := map[string]int64{}
	domSecs := map[string]int64{}
	var total int64
	for _, e := range events {
		switch e.Kind {
		case event.Focus:
			appSecs[e.App] += e.DurS
			total += e.DurS
		case event.URL:
			if d := domainOf(e.URL); d != "" {
				domSecs[d] += e.DurS
			}
		}
	}
	var topics []TopicStat
	for _, kv := range topN(appSecs, 5) {
		topics = append(topics, TopicStat{Label: kv.key, Hours: float64(kv.val) / 3600, Detail: "app"})
	}
	for _, kv := range topN(domSecs, 3) {
		topics = append(topics, TopicStat{Label: kv.key, Hours: float64(kv.val) / 3600, Detail: "web"})
	}
	return topics, float64(total) / 3600
}

// recommend derives actionable, deterministic suggestions from the assembled
// review — never invented, always traceable to a number above.
func recommend(r WeeklyReview, now time.Time) []string {
	var recs []string
	stale := 0
	for _, u := range r.Unfinished {
		if age := int(now.Sub(time.Unix(u.TS, 0)).Hours() / 24); age >= 7 {
			stale++
		}
	}
	if stale > 0 {
		recs = append(recs, fmt.Sprintf("%d loop(s) have been open over a week — close, delegate, or drop them", stale))
	}
	if r.Stats.Meetings >= 15 {
		recs = append(recs, fmt.Sprintf("Heavy meeting week (%d) — consider protecting a focus block", r.Stats.Meetings))
	}
	if r.Stats.Commits == 0 && r.Stats.NotesCreated == 0 && r.Stats.LoopsClosed == 0 {
		recs = append(recs, "Quiet week on record — nothing shipped, closed, or written down")
	}
	if len(r.Deadlines) > 0 && r.Deadlines[0].TS > 0 {
		recs = append(recs, "Next up: "+r.Deadlines[0].Text+" ("+r.Deadlines[0].When+")")
	}
	if len(r.People) > 0 {
		recs = append(recs, fmt.Sprintf("Most involved: %s — worth a check-in?", r.People[0].Name))
	}
	return recs
}

// Headline is a one-line summary of the week, for a notification or subject line.
func (r WeeklyReview) Headline() string {
	return fmt.Sprintf("Week in review: %d done, %d open, %d meetings, %.0fh active",
		len(r.Accomplished), r.Stats.LoopsOpen, r.Stats.Meetings, r.Stats.ActiveHours)
}

// --- helpers ---

type createdNote struct {
	title, kind string
	ts          int64
}

// notesCreatedBetween returns notes whose first_seen falls in the window — the
// week's new people, projects, and topics, i.e. what got written down.
func notesCreatedBetween(db *sql.DB, from, to int64) ([]createdNote, error) {
	rows, err := db.Query(
		`SELECT title, kind, first_seen FROM notes WHERE first_seen >= ? AND first_seen <= ? ORDER BY first_seen DESC`,
		from, to)
	if err != nil {
		return nil, nil // no notes table: nothing created
	}
	defer rows.Close()
	var out []createdNote
	for rows.Next() {
		var n createdNote
		if rows.Scan(&n.title, &n.kind, &n.ts) == nil {
			out = append(out, n)
		}
	}
	return out, rows.Err()
}

type personName struct {
	canonical string
	terms     []string // lowercased title + aliases
}

// loadPersonNames reads people notes so meetings can be attributed to real
// people. Missing notes table → no people, not an error.
func loadPersonNames(db *sql.DB) []personName {
	rows, err := db.Query(`SELECT slug, title FROM notes WHERE kind = 'person'`)
	if err != nil {
		return nil
	}
	type pn struct{ slug, title string }
	var raw []pn
	for rows.Next() {
		var p pn
		if rows.Scan(&p.slug, &p.title) == nil {
			raw = append(raw, p)
		}
	}
	rows.Close()

	aliases := map[string][]string{}
	if arows, err := db.Query(`SELECT slug, alias FROM aliases`); err == nil {
		for arows.Next() {
			var slug, alias string
			if arows.Scan(&slug, &alias) == nil {
				aliases[slug] = append(aliases[slug], alias)
			}
		}
		arows.Close()
	}

	var out []personName
	for _, p := range raw {
		name := p.title
		if name == "" {
			name = trailingSeg(p.slug)
		}
		terms := []string{strings.ToLower(name)}
		for _, a := range aliases[p.slug] {
			terms = append(terms, strings.ToLower(a))
		}
		out = append(out, personName{canonical: name, terms: terms})
	}
	return out
}

// matchPeople returns the canonical names of any people named (whole-word) in a
// title.
func matchPeople(title string, persons []personName) []string {
	padded := " " + strings.ToLower(title) + " "
	var out []string
	for _, p := range persons {
		for _, term := range p.terms {
			if len(term) >= 3 && wholeWordIn(padded, term) {
				out = append(out, p.canonical)
				break
			}
		}
	}
	return out
}

func wholeWordIn(paddedLower, term string) bool {
	i := strings.Index(paddedLower, term)
	for i >= 1 {
		before := paddedLower[i-1]
		after := paddedLower[i+len(term)]
		if !isWordByte(before) && !isWordByte(after) {
			return true
		}
		nx := strings.Index(paddedLower[i+1:], term)
		if nx < 0 {
			return false
		}
		i = i + 1 + nx
	}
	return false
}

func isWordByte(b byte) bool { return b >= 'a' && b <= 'z' || b >= '0' && b <= '9' }

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

// repoName pulls a repo name from a commit event: the path's last segment if
// present, else the app.
func repoName(e event.Event) string {
	if e.Path != "" {
		if seg := trailingSeg(e.Path); seg != "" {
			return seg
		}
	}
	if e.App != "" {
		return e.App
	}
	return "a repo"
}

func trailingSeg(p string) string {
	p = strings.TrimRight(p, "/")
	if i := strings.LastIndexAny(p, "/\\"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// domainOf reduces a URL to its host, dropping scheme, "www." and path.
func domainOf(url string) string {
	s := url
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimPrefix(s, "www.")
	return s
}

type kv struct {
	key string
	val int64
}

// topN returns the n largest map entries, descending, ties broken by key for
// determinism.
func topN(m map[string]int64, n int) []kv {
	var all []kv
	for k, v := range m {
		if k == "" || v == 0 {
			continue
		}
		all = append(all, kv{k, v})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].val != all[j].val {
			return all[i].val > all[j].val
		}
		return all[i].key < all[j].key
	})
	if len(all) > n {
		all = all[:n]
	}
	return all
}
