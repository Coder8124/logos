package project

import (
	"database/sql"
	"testing"

	"github.com/pragun/brain/internal/memory"
	_ "modernc.org/sqlite"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	// Minimal index + memory schema.
	stmts := []string{
		`CREATE TABLE notes (slug TEXT PRIMARY KEY, path TEXT, title TEXT, kind TEXT, body TEXT, hash TEXT, first_seen INTEGER DEFAULT 0)`,
		`CREATE TABLE aliases (slug TEXT, alias TEXT)`,
		`CREATE TABLE edges (src_slug TEXT, pred TEXT, obj TEXT, conf REAL, src TEXT)`,
		`CREATE TABLE events (id INTEGER PRIMARY KEY, ts INTEGER, kind TEXT, app TEXT, title TEXT, url TEXT, path TEXT, dur_s INTEGER DEFAULT 0)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
	if err := memory.Init(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func seedProject(t *testing.T, db *sql.DB) {
	t.Helper()
	// Real notes have real newlines; build the body with them so goal extraction
	// sees the heading and bullets.
	body := "A mapping tool.\n\n## Goals\n- ship the beta\n- reach 100 users\n"
	db.Exec(`INSERT INTO notes (slug,title,kind,body,first_seen) VALUES ('projects/atlas','Atlas','project',?, 1000)`, body)
	db.Exec(`INSERT INTO aliases (slug,alias) VALUES ('projects/atlas','AtlasApp')`)
	db.Exec(`INSERT INTO notes (slug,title,kind,body,first_seen) VALUES ('people/dana','Dana','person','', 1100)`)
	db.Exec(`INSERT INTO edges (src_slug,pred,obj,conf,src) VALUES ('people/dana','works_on','atlas',1.0,'typed')`)
	// A stray note that names the project should be pulled in as related.
	db.Exec(`INSERT INTO notes (slug,title,kind,body,first_seen) VALUES ('daily/2026-07-20','2026-07-20','note','Made progress on Atlas today', 1200)`)
	// A file event mentioning the project.
	db.Exec(`INSERT INTO events (ts,kind,app,title,url,path) VALUES (1300,'file','Code','atlas main','', '/src/atlas/main.go')`)
}

func TestDetectAssemblesDossier(t *testing.T) {
	db := testDB(t)
	seedProject(t, db)
	memory.Store(db, nil, "", &memory.Memory{Text: "Atlas beta must be private by default", Kind: memory.Context, Source: "conversation"})
	memory.Store(db, nil, "", &memory.Memory{Text: "unrelated tea preference", Kind: memory.Preference, Source: "manual"})

	ps, err := Detect(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 1 {
		t.Fatalf("want 1 project, got %d", len(ps))
	}
	p := ps[0]
	if p.Name != "Atlas" {
		t.Errorf("name = %q", p.Name)
	}
	if len(p.Goals) < 2 || !containsStr(p.Goals, "ship the beta") {
		t.Errorf("goals not extracted from the ## Goals section: %v", p.Goals)
	}
	if !hasPerson(p, "Dana") {
		t.Errorf("Dana (linked via edge) should be listed as a person: %+v", p.People)
	}
	if !hasMemoryText(p, "Atlas beta must be private by default") {
		t.Errorf("project memory should include the Atlas-naming memory: %+v", p.Memories)
	}
	if hasMemoryText(p, "unrelated tea preference") {
		t.Error("unrelated memory leaked into the project")
	}
	if len(p.Files) == 0 {
		t.Error("the atlas source file event should surface as a file")
	}
	if p.Convos != 1 {
		t.Errorf("one conversation-sourced memory expected, got %d", p.Convos)
	}
}

func TestAutoScopeTagsSoleProject(t *testing.T) {
	db := testDB(t)
	seedProject(t, db)
	// One memory names Atlas only → should be scoped. One names nothing → global.
	memory.Store(db, nil, "", &memory.Memory{Text: "Atlas ships Friday", Kind: memory.Context, Source: "manual"})
	memory.Store(db, nil, "", &memory.Memory{Text: "I prefer dark mode", Kind: memory.Preference, Source: "manual"})

	n, err := AutoScope(db)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected 1 memory scoped, got %d", n)
	}
	var proj string
	db.QueryRow("SELECT project FROM memories WHERE text = 'Atlas ships Friday'").Scan(&proj)
	if proj != "projects/atlas" {
		t.Errorf("Atlas memory should be scoped to projects/atlas, got %q", proj)
	}
	var global string
	db.QueryRow("SELECT project FROM memories WHERE text = 'I prefer dark mode'").Scan(&global)
	if global != "" {
		t.Errorf("unrelated memory should stay global, got %q", global)
	}
}

func TestWholeWordMatching(t *testing.T) {
	if !mentionsAny("working on Atlas today", []string{"atlas"}) {
		t.Error("should match a whole word")
	}
	if mentionsAny("the atlasphere spins", []string{"atlas"}) {
		t.Error("must not match inside a larger word")
	}
}

func containsStr(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
func hasPerson(p Project, title string) bool {
	for _, r := range p.People {
		if r.Title == title {
			return true
		}
	}
	return false
}
func hasMemoryText(p Project, text string) bool {
	for _, m := range p.Memories {
		if m.Text == text {
			return true
		}
	}
	return false
}
