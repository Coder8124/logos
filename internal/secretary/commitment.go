// Package secretary turns what brain knows into what brain says first.
//
// Everything else in the system is reactive — you ask, you review, you scroll.
// This package is the initiative: it tracks the loops you have left open and
// composes the briefing the app leads with, so the tool tells you what matters
// before you think to ask.
package secretary

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/pragun/brain/internal/router"
)

// A Commitment is something you said you would do that is not yet done. The
// core secretary primitive — an archive does not care what you promised, a
// secretary tracks exactly that.
type Commitment struct {
	ID      int64
	Text    string
	Who     string // person it involves, if any
	Created int64
	DueHint string // free text: "friday", "eod", "" if none
	Status  Status
	// SourceRef points at where it came from (a daily note slug, an event id),
	// so a surfaced loop can always be traced back — same discipline as
	// proposal evidence.
	SourceRef string
	// ResolvedAt is when the loop was closed (done or dropped), 0 while open.
	ResolvedAt int64
}

type Status string

const (
	Open Status = "open"
	Done Status = "done"
	// Dropped: the user dismissed it. Retained, not deleted, so the same loop
	// is not re-extracted and re-surfaced next week.
	Dropped Status = "dropped"
)

const Schema = `
CREATE TABLE IF NOT EXISTS commitments (
    id         INTEGER PRIMARY KEY,
    text       TEXT NOT NULL,
    who        TEXT,
    created    INTEGER NOT NULL,
    due_hint   TEXT,
    status     TEXT NOT NULL DEFAULT 'open',
    source_ref TEXT,
    fingerprint TEXT UNIQUE,
    resolved_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS commitments_status ON commitments(status, created);
`

func Init(db *sql.DB) error {
	if _, err := db.Exec(Schema); err != nil {
		return err
	}
	// Migration for stores created before the weekly review needed to know when
	// a loop was closed.
	db.Exec("ALTER TABLE commitments ADD COLUMN resolved_at INTEGER NOT NULL DEFAULT 0")
	return nil
}

// fingerprint dedupes a loop across re-extractions. Normalised text plus the
// person, so "email Sarah the deck" seen on Tuesday and again Wednesday is one
// commitment, not two.
func fingerprint(text, who string) string {
	norm := strings.ToLower(strings.Join(strings.Fields(text), " "))
	return strings.ToLower(who) + "\x00" + norm
}

func Add(db *sql.DB, c *Commitment) (bool, error) {
	if c.Created == 0 {
		c.Created = time.Now().Unix()
	}
	if c.Status == "" {
		c.Status = Open
	}
	// INSERT OR IGNORE on the fingerprint makes re-running extraction safe and
	// idempotent, which matters because the daily rollup will call it often.
	res, err := db.Exec(
		`INSERT OR IGNORE INTO commitments (text, who, created, due_hint, status, source_ref, fingerprint)
		 VALUES (?,?,?,?,?,?,?)`,
		c.Text, c.Who, c.Created, c.DueHint, string(c.Status), c.SourceRef, fingerprint(c.Text, c.Who))
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		c.ID, _ = res.LastInsertId()
	}
	return n > 0, nil
}

func Open_(db *sql.DB) ([]Commitment, error) { return list(db, Open) }

func list(db *sql.DB, status Status) ([]Commitment, error) {
	rows, err := db.Query(
		`SELECT id, text, who, created, due_hint, status, source_ref
		 FROM commitments WHERE status = ? ORDER BY created`, string(status))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Commitment
	for rows.Next() {
		var c Commitment
		var who, due, src sql.NullString
		var st string
		if err := rows.Scan(&c.ID, &c.Text, &who, &c.Created, &due, &st, &src); err != nil {
			return nil, err
		}
		c.Who, c.DueHint, c.SourceRef, c.Status = who.String, due.String, src.String, Status(st)
		out = append(out, c)
	}
	return out, rows.Err()
}

func SetStatus(db *sql.DB, id int64, s Status) error {
	// Stamp the resolution time when a loop closes (done or dropped), and clear
	// it if a loop is reopened, so the weekly review can report what got closed.
	resolved := int64(0)
	if s != Open {
		resolved = time.Now().Unix()
	}
	_, err := db.Exec("UPDATE commitments SET status = ?, resolved_at = ? WHERE id = ?", string(s), resolved, id)
	return err
}

func OpenCount(db *sql.DB) (int, error) {
	var n int
	err := db.QueryRow("SELECT COUNT(*) FROM commitments WHERE status = 'open'").Scan(&n)
	return n, err
}

// ResolvedSince lists commitments marked done in [since, until], newest first —
// the accomplishments half of the weekly review.
func ResolvedSince(db *sql.DB, since, until int64) ([]Commitment, error) {
	rows, err := db.Query(
		`SELECT id, text, who, created, due_hint, status, source_ref, resolved_at
		 FROM commitments WHERE status = 'done' AND resolved_at >= ? AND resolved_at <= ?
		 ORDER BY resolved_at DESC`, since, until)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Commitment
	for rows.Next() {
		var c Commitment
		var who, due, src sql.NullString
		var st string
		if err := rows.Scan(&c.ID, &c.Text, &who, &c.Created, &due, &st, &src, &c.ResolvedAt); err != nil {
			return nil, err
		}
		c.Who, c.DueHint, c.SourceRef, c.Status = who.String, due.String, src.String, Status(st)
		out = append(out, c)
	}
	return out, rows.Err()
}

// Age is how long a loop has been open, for surfacing the stalest first.
func (c Commitment) Age() time.Duration {
	return time.Since(time.Unix(c.Created, 0))
}

// ---- extraction ----

var commitmentSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"commitments": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"text": map[string]any{"type": "string"},
					"who":  map[string]any{"type": "string"},
					"due":  map[string]any{"type": "string"},
				},
				"required":             []string{"text", "who", "due"},
				"additionalProperties": false,
			},
		},
	},
	"required":             []string{"commitments"},
	"additionalProperties": false,
}

// Extract pulls open loops out of a chunk of text — a daily note, a captured
// message draft. Constrained decoding, T1, one narrow job.
//
// The prompt is tight on purpose: a secretary that invents commitments you
// never made is worse than one that misses a few, because you learn to distrust
// it. Recall is sacrificed for precision here deliberately.
func Extract(rt *router.Router, text, sourceRef string) ([]Commitment, error) {
	model, err := rt.ModelFor(router.T1, true)
	if err != nil {
		return nil, err
	}

	const system = "Extract only explicit commitments the author made to do something later — " +
		"'I'll send…', 'need to…', 'todo:', 'follow up with…', 'waiting on…'. " +
		"Reply with JSON only. Copy the task text nearly verbatim; do not invent tasks, " +
		"do not include things already done, do not include vague intentions. " +
		"Set who to the person involved or an empty string. Set due to any stated " +
		"timeframe ('friday', 'eod') or an empty string. Return an empty list if there " +
		"are no explicit commitments."

	out, err := rt.Local().Chat(model, system, text, commitmentSchema)
	if err != nil {
		return nil, err
	}

	var res struct {
		Commitments []struct {
			Text string `json:"text"`
			Who  string `json:"who"`
			Due  string `json:"due"`
		} `json:"commitments"`
	}
	if err := json.Unmarshal([]byte(cleanJSON(out)), &res); err != nil {
		return nil, fmt.Errorf("commitment extraction returned unparseable JSON: %w", err)
	}

	var out2 []Commitment
	for _, c := range res.Commitments {
		text := strings.TrimSpace(c.Text)
		if len(text) < 4 {
			continue
		}
		out2 = append(out2, Commitment{
			Text: text, Who: strings.TrimSpace(c.Who),
			DueHint: strings.TrimSpace(c.Due), SourceRef: sourceRef,
		})
	}
	return out2, nil
}

func cleanJSON(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		}
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	}
	return strings.TrimSpace(s)
}
