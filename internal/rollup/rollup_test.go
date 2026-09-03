package rollup

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Coder8124/brain/internal/event"
	"gopkg.in/yaml.v3"
	_ "modernc.org/sqlite"
)

// ---------- sessions ----------

func focus(ts, dur int64, app string) event.Event {
	return event.Event{ID: ts, TS: ts, Kind: event.Focus, App: app, Title: app + " window", DurS: dur}
}

func TestSessioniseSplitsOnIdleGap(t *testing.T) {
	events := []event.Event{
		focus(0, 600, "Ghostty"),
		focus(600, 600, "Chrome"),
		// 40 minute gap — a new session.
		focus(1200+40*60, 300, "Ghostty"),
	}
	got := Sessionise(events)
	if len(got) != 2 {
		t.Fatalf("got %d sessions, want 2", len(got))
	}
	if len(got[0].Events) != 2 || len(got[1].Events) != 1 {
		t.Errorf("events split wrongly: %d / %d", len(got[0].Events), len(got[1].Events))
	}
}

func TestSessioniseDropsIncidentalFlickers(t *testing.T) {
	events := []event.Event{
		focus(0, 600, "Ghostty"),
		focus(700, 2, "Finder"), // below IncidentalSecs
	}
	got := Sessionise(events)
	if len(got) != 1 || len(got[0].Events) != 1 {
		t.Fatalf("flicker should be dropped, got %+v", got)
	}
}

func TestSessionCarriesEvidence(t *testing.T) {
	got := Sessionise([]event.Event{focus(10, 600, "Ghostty"), focus(700, 600, "Chrome")})
	if len(got) != 1 {
		t.Fatalf("got %d sessions", len(got))
	}
	if len(got[0].EventIDs) != 2 {
		t.Errorf("evidence = %v, want 2 ids", got[0].EventIDs)
	}
}

func TestSessioniseEmptyInput(t *testing.T) {
	if got := Sessionise(nil); got != nil {
		t.Errorf("empty input should yield no sessions, got %+v", got)
	}
}

func TestDigestDeduplicatesAndCaps(t *testing.T) {
	var events []event.Event
	for i := int64(0); i < 30; i++ {
		events = append(events, event.Event{
			ID: i, TS: i * 10, Kind: event.URL, App: "chrome",
			URL: "https://example.com/page", DurS: 5,
		})
	}
	s := Sessionise(events)[0]
	d := s.Digest(5)

	if strings.Count(d, "example.com") != 1 {
		t.Errorf("identical hosts should collapse to one entry:\n%s", d)
	}
}

// ---------- proposals ----------

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if err := InitQueue(db); err != nil {
		t.Fatal(err)
	}
	db.Exec(`CREATE TABLE notes (slug TEXT PRIMARY KEY, path TEXT, title TEXT, kind TEXT, body TEXT, hash TEXT)`)
	db.Exec(`CREATE TABLE aliases (slug TEXT, alias TEXT)`)
	t.Cleanup(func() { db.Close() })
	return db
}

func TestProposalWithoutEvidenceIsRefused(t *testing.T) {
	db := testDB(t)
	p := &Proposal{Kind: NewNote, Target: "people/sam", Conf: 0.8,
		Payload: Payload{Body: "x", Type: "person"}}

	if err := Enqueue(db, p); err == nil {
		t.Fatal("a proposal with no evidence must be impossible to queue")
	}
}

func TestProposalValidationCatchesMalformedKinds(t *testing.T) {
	cases := []struct {
		name string
		p    Proposal
	}{
		{"empty body", Proposal{Kind: NewNote, Target: "a", Conf: 0.5, Evidence: []int64{1}}},
		{"edge without pred", Proposal{Kind: NewEdge, Target: "a", Conf: 0.5, Evidence: []int64{1}, Payload: Payload{Obj: "b"}}},
		{"merge without into", Proposal{Kind: Merge, Target: "a", Conf: 0.5, Evidence: []int64{1}}},
		{"confidence out of range", Proposal{Kind: Append, Target: "a", Conf: 5, Evidence: []int64{1}, Payload: Payload{Body: "x"}}},
		{"unknown kind", Proposal{Kind: "wat", Target: "a", Conf: 0.5, Evidence: []int64{1}}},
	}
	for _, c := range cases {
		if err := c.p.Validate(); err == nil {
			t.Errorf("%s should fail validation", c.name)
		}
	}
}

func TestQueueRoundTripAndStatus(t *testing.T) {
	db := testDB(t)
	p := &Proposal{Kind: NewNote, Target: "people/sam", Conf: 0.8, Evidence: []int64{1, 2},
		Payload: Payload{Title: "Sam", Type: "person", Body: "worked on the api"}, Model: "gemma3:4b"}

	if err := Enqueue(db, p); err != nil {
		t.Fatal(err)
	}

	pending, err := List(db, Pending)
	if err != nil || len(pending) != 1 {
		t.Fatalf("List = %d, %v", len(pending), err)
	}
	if len(pending[0].Evidence) != 2 || pending[0].Payload.Title != "Sam" {
		t.Errorf("round trip lost data: %+v", pending[0])
	}

	// Rejections are retained, not deleted — they are the signal for tuning
	// the auto-accept threshold.
	if err := SetStatus(db, p.ID, Rejected); err != nil {
		t.Fatal(err)
	}
	if n, _ := PendingCount(db); n != 0 {
		t.Errorf("pending = %d, want 0", n)
	}
	if rejected, _ := List(db, Rejected); len(rejected) != 1 {
		t.Error("rejected proposals must be retained")
	}
}

// ---------- resolution ----------

func TestResolveMatchesSlugAndAlias(t *testing.T) {
	db := testDB(t)
	db.Exec(`INSERT INTO notes (slug, kind, title) VALUES ('people/sameer','person','Sameer')`)
	db.Exec(`INSERT INTO aliases (slug, alias) VALUES ('people/sameer','Sam')`)

	if m, ok := Resolve(db, "sameer", "person"); !ok || m.Slug != "people/sameer" {
		t.Errorf("slug match failed: %+v %v", m, ok)
	}
	if m, ok := Resolve(db, "Sam", "person"); !ok || m.Slug != "people/sameer" {
		t.Errorf("alias match failed: %+v %v", m, ok)
	}
	if _, ok := Resolve(db, "nobody", "person"); ok {
		t.Error("unknown name should not resolve")
	}
}

func TestNearMissCatchesLikelyDuplicates(t *testing.T) {
	db := testDB(t)
	db.Exec(`INSERT INTO notes (slug, kind, title) VALUES ('people/sameer','person','Sameer')`)

	if got := NearMiss(db, "Sam", "person"); len(got) == 0 {
		t.Error("prefix should be a near miss — this is what stops duplicate people")
	}
	if got := NearMiss(db, "Samher", "person"); len(got) == 0 {
		t.Error("single edit should be a near miss")
	}
	if got := NearMiss(db, "Ana", "person"); len(got) != 0 {
		t.Errorf("unrelated name should not match: %v", got)
	}
}

// ---------- applying ----------

func TestApplyNewNoteWritesValidFrontmatter(t *testing.T) {
	dir := t.TempDir()
	p := Proposal{Kind: NewNote, Target: "people/sam", Conf: 0.8, Evidence: []int64{1},
		Payload: Payload{Title: "Sam", Type: "person", Body: "worked on the api"}, Created: 1752883200}

	if err := Apply(nil, dir, p); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "people", "sam.md"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	for _, want := range []string{"type: person", "title: Sam", "first_seen:", ObservationsHeading, "worked on the api"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
}

func TestApplyNewNoteRefusesToClobber(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "people"), 0o755)
	os.WriteFile(filepath.Join(dir, "people", "sam.md"), []byte("hand written"), 0o644)

	p := Proposal{Kind: NewNote, Target: "people/sam", Conf: 0.8, Evidence: []int64{1},
		Payload: Payload{Type: "person", Body: "x"}}

	if err := Apply(nil, dir, p); err == nil {
		t.Fatal("must refuse to overwrite an existing note")
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "people", "sam.md"))
	if string(raw) != "hand written" {
		t.Error("existing content was modified")
	}
}

func TestApplyAppendLeavesUserProseIntact(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "people"), 0o755)
	original := "---\ntype: person\n---\n\nSam is a colleague I trust.\n\n" + ObservationsHeading + "\n\n- 2026-07-01 earlier note\n"
	path := filepath.Join(dir, "people", "sam.md")
	os.WriteFile(path, []byte(original), 0o644)

	p := Proposal{Kind: Append, Target: "people/sam", Conf: 0.8, Evidence: []int64{1},
		Payload: Payload{Body: "reviewed the api PR"}, Created: 1752883200}

	if err := Apply(nil, dir, p); err != nil {
		t.Fatal(err)
	}

	got, _ := os.ReadFile(path)
	s := string(got)
	if !strings.Contains(s, "Sam is a colleague I trust.") {
		t.Error("hand-written prose must be preserved verbatim")
	}
	if !strings.Contains(s, "earlier note") {
		t.Error("previous observations must be preserved")
	}
	if !strings.Contains(s, "reviewed the api PR") {
		t.Error("new observation missing")
	}
}

func TestApplyEdgeAddsTypedRelation(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "people"), 0o755)
	path := filepath.Join(dir, "people", "sam.md")
	os.WriteFile(path, []byte("---\ntype: person\n---\n\nbody\n"), 0o644)

	p := Proposal{Kind: NewEdge, Target: "people/sam", Conf: 0.82, Evidence: []int64{1},
		Payload: Payload{Pred: "works_on", Obj: "brain"}}

	if err := Apply(nil, dir, p); err != nil {
		t.Fatal(err)
	}

	got, _ := os.ReadFile(path)
	s := string(got)
	if !strings.Contains(s, "relations:") || !strings.Contains(s, "works_on") {
		t.Errorf("relation not added:\n%s", s)
	}
	if !strings.Contains(s, "src: inferred") {
		t.Error("inferred edges must record their provenance")
	}
	if !strings.Contains(s, "conf: 0.82") {
		t.Error("edge must carry its confidence")
	}
	if !strings.Contains(s, "type: person") || !strings.Contains(s, "body") {
		t.Error("existing frontmatter and body must survive")
	}
}

func TestMergeIsNotAppliedAutomatically(t *testing.T) {
	p := Proposal{Kind: Merge, Target: "people/sam", Conf: 0.4, Evidence: []int64{1},
		Payload: Payload{Into: "people/sameer"}}

	if err := Apply(nil, t.TempDir(), p); err == nil {
		t.Fatal("merges must not be applied automatically — they destroy information when wrong")
	}
}

// ---------- confidence ----------

func TestConfidenceReflectsEvidenceWeight(t *testing.T) {
	long := Session{Start: 0, End: 7200}
	short := Session{Start: 0, End: 120}

	work := Classification{Category: Work}
	if confidenceFor(work, long) <= confidenceFor(work, short) {
		t.Error("a two-hour session should outrank a two-minute one")
	}
	if c := confidenceFor(work, long); c > 0.95 {
		t.Errorf("confidence must stay below certainty, got %v", c)
	}
}

func TestNoiseNamesAreFiltered(t *testing.T) {
	for _, n := range []string{"Chrome", "github", "  ", "a"} {
		if !isNoise(n) {
			t.Errorf("%q should be filtered as noise", n)
		}
	}
	if isNoise("Sameer") {
		t.Error("real names must not be filtered")
	}
}

func TestDomainsAreRejectedAsEntities(t *testing.T) {
	for _, n := range []string{
		"google.com", "discord.com", "en.wikipedia.org", "democracybot.vercel.app",
		"https://example.com/x", "discord.gg",
	} {
		if !isNoise(n) {
			t.Errorf("%q is a domain and must not become a note", n)
		}
	}
}

func TestRealNamesEndingInTLDLettersSurvive(t *testing.T) {
	// A filter that strips trailing "co"/"ai"/"dev" would silently swallow
	// legitimate entity names. Domains are rejected by their dot, not their
	// last two letters.
	for _, n := range []string{"Cisco", "Mumbai", "Waco", "Devi", "Coco"} {
		if isNoise(n) {
			t.Errorf("%q is a legitimate name and must survive the filter", n)
		}
	}
}

func TestDigestForEntitiesOmitsHosts(t *testing.T) {
	events := []event.Event{
		{ID: 1, TS: 0, Kind: event.URL, App: "chrome", URL: "https://discord.com/x", DurS: 60},
		{ID: 2, TS: 100, Kind: event.Focus, App: "Ghostty", Title: "reviewing the API design", DurS: 600},
	}
	d := Sessionise(events)[0].DigestForEntities(10)

	if strings.Contains(d, "discord.com") {
		t.Errorf("host list must not reach the entity extractor:\n%s", d)
	}
	if !strings.Contains(d, "reviewing the API design") {
		t.Errorf("window titles must reach the entity extractor:\n%s", d)
	}
}

// ---------- what a proposal is not allowed to do ----------

// A proposal's target is model output: it is written by a small local model
// reading captured text that came from web pages, other people's messages and
// anything else on the clipboard. Joining it onto the vault path unchecked made
// "accept" a way to write a file anywhere the user can write.
func TestAProposalCannotWriteOutsideTheVault(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Dir(dir)

	for _, target := range []string{
		"../escaped",
		"people/../../escaped",
		"people/../../../escaped",
	} {
		p := Proposal{Kind: NewNote, Target: target, Conf: 0.8, Evidence: []int64{1},
			Payload: Payload{Type: "person", Body: "x"}}

		if err := p.Validate(); err == nil {
			t.Errorf("target %q queued without complaint", target)
		}
		if err := Apply(nil, dir, p); err == nil {
			t.Errorf("target %q was applied", target)
		}
		if _, err := os.Stat(filepath.Join(outside, "escaped.md")); err == nil {
			t.Fatalf("target %q wrote outside the vault", target)
		}
	}
}

// An absolute target is the same escape by another spelling.
func TestAnAbsoluteTargetIsRefused(t *testing.T) {
	p := Proposal{Kind: NewNote, Target: "/etc/brain", Conf: 0.8, Evidence: []int64{1},
		Payload: Payload{Type: "person", Body: "x"}}
	if err := p.Validate(); err == nil {
		t.Fatal("an absolute target was accepted")
	}
}

// The title is model output too, and it is written straight into the note's
// frontmatter. A newline in it ends the frontmatter block early, so everything
// the proposal says after that is parsed as note body — or as further keys.
func TestATitleCannotForgeFrontmatter(t *testing.T) {
	dir := t.TempDir()
	p := Proposal{Kind: NewNote, Target: "people/sam", Conf: 0.8, Evidence: []int64{1},
		Payload: Payload{Title: "Sam\npinned: true\n---\ninjected body", Type: "person",
			Body: "worked on the api"}, Created: 1752883200}

	// Refusing it and writing it safely are both acceptable; writing a note
	// whose frontmatter the title invented is not.
	if err := Apply(nil, dir, p); err != nil {
		return
	}
	raw, err := os.ReadFile(filepath.Join(dir, "people", "sam.md"))
	if err != nil {
		t.Fatal(err)
	}
	fm, _, _ := strings.Cut(strings.TrimPrefix(string(raw), "---\n"), "\n---")
	if strings.Contains(fm, "pinned: true") {
		t.Errorf("the title forged a frontmatter key:\n%s", raw)
	}
	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(fm), &parsed); err != nil {
		t.Errorf("the title broke the frontmatter: %v\n%s", err, raw)
	}
}

// The same hole on the edge path, where pred and obj are interpolated into a
// YAML sequence entry.
func TestAnEdgeCannotForgeFrontmatter(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "people"), 0o755)
	os.WriteFile(filepath.Join(dir, "people", "sam.md"),
		[]byte("---\ntype: person\n---\n\nprose\n"), 0o644)

	p := Proposal{Kind: NewEdge, Target: "people/sam", Conf: 0.9, Evidence: []int64{1},
		Payload: Payload{Pred: "works_at", Obj: "acme\"]] }\npinned: true\nx: \"[[y"}}

	if err := Apply(nil, dir, p); err != nil {
		return
	}
	raw, err := os.ReadFile(filepath.Join(dir, "people", "sam.md"))
	if err != nil {
		t.Fatal(err)
	}
	fm, _, _ := strings.Cut(strings.TrimPrefix(string(raw), "---\n"), "\n---")
	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(fm), &parsed); err != nil {
		t.Errorf("the edge broke the frontmatter: %v\n%s", err, raw)
	}
	if _, forged := parsed["pinned"]; forged {
		t.Errorf("the edge forged a frontmatter key:\n%s", raw)
	}
}
