// Package project auto-detects the pieces of work a life is organised around and
// assembles a dossier for each — its notes, the people involved, the files it
// touches, its goals, and recent progress — without the user filing anything.
//
// Detection needs no manual step: the rollup pipeline already distils captured
// activity into project notes in the vault, and this package treats every such
// note as a project, then pulls together everything connected to it from the
// same index the rest of the system uses — the note graph, the memory store, and
// the raw event log. The result is the "what's going on with X" view a good
// chief-of-staff keeps in their head.
package project

import (
	"database/sql"
	"sort"
	"strings"
	"time"

	"github.com/pragun/brain/internal/memory"
)

// Ref is a lightweight pointer to a note.
type Ref struct {
	Slug  string `json:"slug"`
	Title string `json:"title"`
	Kind  string `json:"kind"`
}

// FileRef is a file the project has touched, aggregated from the event log.
type FileRef struct {
	Path   string `json:"path"`
	Count  int    `json:"count"`
	LastTS int64  `json:"last_ts"`
}

// ProgressItem is one recent sign of life on the project.
type ProgressItem struct {
	TS   int64  `json:"ts"`
	Kind string `json:"kind"` // event | note | memory
	Text string `json:"text"`
}

// Project is the assembled dossier.
type Project struct {
	Slug       string          `json:"slug"`
	Name       string          `json:"name"`
	Aliases    []string        `json:"aliases,omitempty"`
	Notes      []Ref           `json:"notes"`
	People     []Ref           `json:"people"`
	Files      []FileRef       `json:"files"`
	Goals      []string        `json:"goals"`
	Progress   []ProgressItem  `json:"progress"`
	Memories   []memory.Memory `json:"memories"`
	Convos     int             `json:"conversations"` // learned-from conversations touching this project
	LastActive int64           `json:"last_active"`
}

// noteRow is a fully-loaded note, read before any follow-up query.
type noteRow struct {
	slug, title, kind, body string
	firstSeen               int64
}

// Detect returns every project with its dossier, most-recently-active first.
func Detect(db *sql.DB) ([]Project, error) {
	notes, err := loadNotes(db)
	if err != nil {
		return nil, err
	}
	aliases := loadAliases(db)
	edges, err := loadEdges(db)
	if err != nil {
		return nil, err
	}
	mems, err := loadMemories(db)
	if err != nil {
		return nil, err
	}

	var projects []Project
	for _, n := range notes {
		if n.kind != "project" {
			continue
		}
		p := assemble(db, n, notes, aliases[n.slug], edges, mems)
		projects = append(projects, p)
	}
	sort.Slice(projects, func(i, j int) bool { return projects[i].LastActive > projects[j].LastActive })
	return projects, nil
}

// Get returns one project's dossier by slug (or trailing segment).
func Get(db *sql.DB, slug string) (Project, bool, error) {
	ps, err := Detect(db)
	if err != nil {
		return Project{}, false, err
	}
	for _, p := range ps {
		if p.Slug == slug || trailing(p.Slug) == slug || strings.EqualFold(p.Name, slug) {
			return p, true, nil
		}
	}
	return Project{}, false, nil
}

// assemble builds one project's dossier from the pre-loaded index data plus a few
// bounded event queries (the event log is too large to load whole).
func assemble(db *sql.DB, seed noteRow, notes []noteRow, aliases []string, edges []edge, mems []memRow) Project {
	p := Project{Slug: seed.slug, Name: displayName(seed), Aliases: aliases}
	terms := append([]string{p.Name}, aliases...)
	// The trailing slug segment is also a usable term ("projects/brain" -> "brain").
	terms = append(terms, trailing(seed.slug))

	// --- connected notes & people, via the note graph ---
	connected := map[string]bool{}
	for _, e := range edges {
		if e.src == seed.slug && !resolvesTo(e.obj, seed.slug) {
			connected[e.obj] = true // outgoing target (trailing form)
		}
		if e.src != seed.slug && resolvesTo(e.obj, seed.slug) {
			connected[e.src] = true // someone links to us
		}
	}
	for _, n := range notes {
		if n.slug == seed.slug {
			continue
		}
		// A note belongs to the project if it is graph-connected or names it.
		if connected[n.slug] || connected[trailing(n.slug)] || mentionsAny(n.body, terms) || mentionsAny(n.title, terms) {
			ref := Ref{Slug: n.slug, Title: displayName(n), Kind: n.kind}
			if n.kind == "person" {
				p.People = append(p.People, ref)
			} else {
				p.Notes = append(p.Notes, ref)
			}
		}
	}

	// --- goals: a "Goals" section of the project note, plus context memories ---
	p.Goals = extractGoals(seed.body)

	// --- memories: tagged to the project, or naming it ---
	var lastActive int64 = seed.firstSeen
	for _, m := range mems {
		belongs := m.project == seed.slug || m.project == trailing(seed.slug)
		if !belongs && m.project == "" && mentionsAny(m.text, terms) {
			belongs = true
		}
		if !belongs {
			continue
		}
		p.Memories = append(p.Memories, memory.Memory{
			ID: m.id, Text: m.text, Kind: memory.Kind(m.kind), Salience: m.salience,
			Confidence: m.confidence, Project: m.project, Source: m.source, Created: m.created,
		})
		if m.source == "conversation" {
			p.Convos++
		}
		if m.created > lastActive {
			lastActive = m.created
		}
		if m.kind == string(memory.Context) {
			if g := strings.TrimSpace(m.text); g != "" {
				p.Goals = append(p.Goals, g)
			}
		}
	}

	// --- files & progress: bounded scan of the event log by name ---
	p.Files, p.Progress = eventSignals(db, terms)
	for _, pr := range p.Progress {
		if pr.TS > lastActive {
			lastActive = pr.TS
		}
	}
	for _, f := range p.Files {
		if f.LastTS > lastActive {
			lastActive = f.LastTS
		}
	}

	// Recent notes count as progress too.
	for _, n := range notes {
		if n.firstSeen > 0 && (mentionsAny(n.body, terms) || n.slug == seed.slug) {
			p.Progress = append(p.Progress, ProgressItem{TS: n.firstSeen, Kind: "note", Text: displayName(n)})
			if n.firstSeen > lastActive {
				lastActive = n.firstSeen
			}
		}
	}
	sort.Slice(p.Progress, func(i, j int) bool { return p.Progress[i].TS > p.Progress[j].TS })
	if len(p.Progress) > 8 {
		p.Progress = p.Progress[:8]
	}
	p.Goals = dedupStrings(p.Goals)
	p.LastActive = lastActive
	return p
}

// AutoScope tags each memory that names exactly one project with that project's
// slug, so project-scoped recall works without the user classifying anything.
// A memory naming several projects is left global (ambiguous ownership).
func AutoScope(db *sql.DB) (int, error) {
	notes, err := loadNotes(db)
	if err != nil {
		return 0, err
	}
	aliases := loadAliases(db)
	type proj struct {
		slug  string
		terms []string
	}
	var projs []proj
	for _, n := range notes {
		if n.kind == "project" {
			terms := append([]string{displayName(n), trailing(n.slug)}, aliases[n.slug]...)
			projs = append(projs, proj{n.slug, terms})
		}
	}
	mems, err := loadMemories(db)
	if err != nil {
		return 0, err
	}

	type set struct {
		id   int64
		slug string
	}
	var updates []set
	for _, m := range mems {
		if m.project != "" {
			continue // already scoped
		}
		var hit string
		matches := 0
		for _, pr := range projs {
			if mentionsAny(m.text, pr.terms) {
				matches++
				hit = pr.slug
			}
		}
		if matches == 1 {
			updates = append(updates, set{m.id, hit})
		}
	}
	for _, u := range updates {
		db.Exec("UPDATE memories SET project = ? WHERE id = ?", u.slug, u.id)
	}
	return len(updates), nil
}

// --- bounded event scan ---

func eventSignals(db *sql.DB, terms []string) ([]FileRef, []ProgressItem) {
	// Pull a bounded, recent slice of events that mention any term, then refine on
	// word boundaries in Go. LIKE is a coarse prefilter; the DB never loads whole.
	files := map[string]*FileRef{}
	var progress []ProgressItem
	seen := map[int64]bool{} // an event can match several terms; count it once
	for _, term := range terms {
		if len(term) < 3 {
			continue
		}
		like := "%" + term + "%"
		rows, err := db.Query(
			`SELECT id, ts, kind, app, title, url, path FROM events
			 WHERE title LIKE ? OR path LIKE ? OR url LIKE ? OR app LIKE ?
			 ORDER BY ts DESC LIMIT 100`, like, like, like, like)
		if err != nil {
			continue
		}
		type ev struct {
			id                     int64
			ts                     int64
			kind, app, title, path string
			url                    string
		}
		var evs []ev
		for rows.Next() {
			var e ev
			if rows.Scan(&e.id, &e.ts, &e.kind, &e.app, &e.title, &e.url, &e.path) == nil {
				evs = append(evs, e)
			}
		}
		rows.Close()

		for _, e := range evs {
			if seen[e.id] {
				continue
			}
			hay := strings.ToLower(e.title + " " + e.path + " " + e.url + " " + e.app)
			if !mentionsAny(hay, []string{term}) {
				continue
			}
			seen[e.id] = true
			if e.path != "" {
				if f := files[e.path]; f != nil {
					f.Count++
					if e.ts > f.LastTS {
						f.LastTS = e.ts
					}
				} else {
					files[e.path] = &FileRef{Path: e.path, Count: 1, LastTS: e.ts}
				}
			}
			label := e.title
			if label == "" {
				label = e.path
			}
			progress = append(progress, ProgressItem{TS: e.ts, Kind: "event", Text: strings.TrimSpace(e.app + " · " + label)})
		}
	}

	var fl []FileRef
	for _, f := range files {
		fl = append(fl, *f)
	}
	sort.Slice(fl, func(i, j int) bool { return fl[i].LastTS > fl[j].LastTS })
	if len(fl) > 10 {
		fl = fl[:10]
	}
	return fl, progress
}

// --- goal extraction from a note body ---

func extractGoals(body string) []string {
	var goals []string
	inGoals := false
	for _, line := range strings.Split(body, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "#") {
			inGoals = strings.Contains(strings.ToLower(t), "goal")
			continue
		}
		if inGoals {
			if b := bulletText(t); b != "" {
				goals = append(goals, b)
			}
		}
	}
	return goals
}

func bulletText(t string) string {
	for _, p := range []string{"- [ ] ", "- [x] ", "- ", "* ", "1. "} {
		if strings.HasPrefix(t, p) {
			return strings.TrimSpace(t[len(p):])
		}
	}
	return ""
}

// --- loaders (each reads its table fully, closing the cursor before returning,
// so callers never nest a query inside an open one on the single-conn pool) ---

func loadNotes(db *sql.DB) ([]noteRow, error) {
	rows, err := db.Query(`SELECT slug, title, kind, body, first_seen FROM notes`)
	if err != nil {
		return nil, nil // no notes table yet: no projects, not an error
	}
	defer rows.Close()
	var out []noteRow
	for rows.Next() {
		var n noteRow
		if rows.Scan(&n.slug, &n.title, &n.kind, &n.body, &n.firstSeen) == nil {
			out = append(out, n)
		}
	}
	return out, rows.Err()
}

func loadAliases(db *sql.DB) map[string][]string {
	m := map[string][]string{}
	rows, err := db.Query(`SELECT slug, alias FROM aliases`)
	if err != nil {
		return m
	}
	defer rows.Close()
	for rows.Next() {
		var slug, alias string
		if rows.Scan(&slug, &alias) == nil {
			m[slug] = append(m[slug], alias)
		}
	}
	return m
}

type edge struct{ src, obj string }

func loadEdges(db *sql.DB) ([]edge, error) {
	rows, err := db.Query(`SELECT src_slug, obj FROM edges`)
	if err != nil {
		return nil, nil
	}
	defer rows.Close()
	var out []edge
	for rows.Next() {
		var e edge
		if rows.Scan(&e.src, &e.obj) == nil {
			out = append(out, e)
		}
	}
	return out, rows.Err()
}

type memRow struct {
	id                   int64
	text, kind, project  string
	source               string
	salience, confidence float64
	created              int64
}

func loadMemories(db *sql.DB) ([]memRow, error) {
	rows, err := db.Query(`SELECT id, text, kind, salience, confidence, project, source, created FROM memories WHERE superseded = 0`)
	if err != nil {
		return nil, nil
	}
	defer rows.Close()
	var out []memRow
	for rows.Next() {
		var m memRow
		if rows.Scan(&m.id, &m.text, &m.kind, &m.salience, &m.confidence, &m.project, &m.source, &m.created) == nil {
			out = append(out, m)
		}
	}
	return out, rows.Err()
}

// --- small helpers ---

func displayName(n noteRow) string {
	if strings.TrimSpace(n.title) != "" {
		return n.title
	}
	return trailing(n.slug)
}

func trailing(slug string) string {
	if i := strings.LastIndex(slug, "/"); i >= 0 {
		return slug[i+1:]
	}
	return slug
}

// resolvesTo reports whether an edge object (a trailing form like "brain")
// resolves to the given full slug ("projects/brain").
func resolvesTo(obj, slug string) bool {
	return obj == slug || obj == trailing(slug)
}

// mentionsAny reports whether text names any term as a whole word (case-insensitive).
func mentionsAny(text string, terms []string) bool {
	padded := " " + strings.ToLower(text) + " "
	for _, term := range terms {
		term = strings.ToLower(strings.TrimSpace(term))
		if len(term) < 3 {
			continue
		}
		if wholeWord(padded, term) {
			return true
		}
	}
	return false
}

func wholeWord(paddedLower, term string) bool {
	i := strings.Index(paddedLower, term)
	for i >= 1 {
		before := paddedLower[i-1]
		after := paddedLower[i+len(term)]
		if !isWord(before) && !isWord(after) {
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

func isWord(b byte) bool { return b >= 'a' && b <= 'z' || b >= '0' && b <= '9' }

func dedupStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if k := strings.ToLower(strings.TrimSpace(s)); k != "" && !seen[k] {
			seen[k] = true
			out = append(out, s)
		}
	}
	return out
}

// Age renders a timestamp as a human "how long ago", for the CLI.
func Age(ts int64) string {
	if ts == 0 {
		return "—"
	}
	d := time.Since(time.Unix(ts, 0))
	switch {
	case d < time.Hour:
		return "just now"
	case d < 24*time.Hour:
		return itoa(int(d.Hours())) + "h ago"
	case d < 30*24*time.Hour:
		return itoa(int(d.Hours()/24)) + "d ago"
	default:
		return itoa(int(d.Hours()/(24*30))) + "mo ago"
	}
}

func itoa(n int) string {
	if n <= 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
