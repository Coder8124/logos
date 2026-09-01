// Package dream is the nightly consolidation pass — brain sleeping on the day.
//
// It runs in two phases. NREM (cheap, deterministic, first) stabilises the
// memory store: it replays the day's salient experience, extracts gist from
// recurring structure, and downscales the whole field so only what is reinforced
// stays prominent. REM (expensive, model-driven, last) recombines the cleaned
// store into candidate connections — the engine behind the mirror.
//
// The house rules hold throughout. Compute, then narrate: what to replay,
// downscale, and bridge is chosen by arithmetic; the model only phrases a
// connection. Propose, don't assert: NREM's structural maintenance is the same
// deterministic upkeep already trusted to run headless, but every REM inference
// is an Insight in this queue — never a silent write.
package dream

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Coder8124/brain/internal/memory"
	"github.com/Coder8124/brain/internal/provider"
)

// Kind is what a dreamed insight would become if accepted.
type Kind string

const (
	// Connection: a non-obvious link between two things the assistant already
	// knows — a shared theme, a tension, or an idea one suggests about the other.
	Connection Kind = "connection"
	// Thread: a candidate line of work the day's activity hints at, synthesised
	// from more than one memory.
	Thread Kind = "thread"
)

type Status string

const (
	Pending  Status = "pending"
	Accepted Status = "accepted"
	Rejected Status = "rejected"
)

// Insight is a REM proposal awaiting review. It is modelled on rollup.Proposal,
// but its terminal is the memory store, not the vault: accepting one stores a
// low-confidence memory that then lives or fades in the ordinary decay loop.
//
// Both endpoints are mandatory and enforced at write time — this is the
// hallucination filter. A connection that cannot name the two real memories it
// bridges is a fabrication with good manners, and it must be impossible to queue.
type Insight struct {
	ID        int64
	Kind      Kind
	Text      string // the synthesised observation, one or two sentences
	EndpointA int64  // memory id it bridges from
	EndpointB int64  // memory id it bridges to
	Conf      float64
	Model     string
	Created   int64
	Status    Status
}

const QueueSchema = `
CREATE TABLE IF NOT EXISTS dream_insights (
    id         INTEGER PRIMARY KEY,
    kind       TEXT NOT NULL,
    text       TEXT NOT NULL,
    endpoint_a INTEGER NOT NULL,
    endpoint_b INTEGER NOT NULL,
    conf       REAL NOT NULL,
    model      TEXT NOT NULL,
    created    INTEGER NOT NULL,
    status     TEXT NOT NULL DEFAULT 'pending'
);
CREATE INDEX IF NOT EXISTS dream_insights_status ON dream_insights(status, created);
`

func InitQueue(db *sql.DB) error {
	_, err := db.Exec(QueueSchema)
	return err
}

// Validate enforces the invariants that make the queue trustworthy — checked on
// every insert, never trusted from the model.
func (in *Insight) Validate() error {
	if strings.TrimSpace(in.Text) == "" {
		return fmt.Errorf("insight has no text")
	}
	if in.EndpointA == 0 || in.EndpointB == 0 {
		return fmt.Errorf("insight must cite two memories — refusing to queue an ungrounded connection")
	}
	if in.EndpointA == in.EndpointB {
		return fmt.Errorf("insight bridges a memory to itself")
	}
	if in.Conf < 0 || in.Conf > 1 {
		return fmt.Errorf("insight confidence %v outside 0..1", in.Conf)
	}
	return nil
}

func Enqueue(db *sql.DB, in *Insight) error {
	if err := in.Validate(); err != nil {
		return err
	}
	if in.Created == 0 {
		in.Created = time.Now().Unix()
	}
	if in.Status == "" {
		in.Status = Pending
	}
	res, err := db.Exec(
		`INSERT INTO dream_insights (kind, text, endpoint_a, endpoint_b, conf, model, created, status)
		 VALUES (?,?,?,?,?,?,?,?)`,
		string(in.Kind), in.Text, in.EndpointA, in.EndpointB, in.Conf, in.Model, in.Created, string(in.Status))
	if err != nil {
		return err
	}
	in.ID, _ = res.LastInsertId()
	return nil
}

func scan(rows *sql.Rows) ([]Insight, error) {
	var out []Insight
	for rows.Next() {
		var in Insight
		var kind, st string
		if err := rows.Scan(&in.ID, &kind, &in.Text, &in.EndpointA, &in.EndpointB, &in.Conf, &in.Model, &in.Created, &st); err != nil {
			return nil, err
		}
		in.Kind, in.Status = Kind(kind), Status(st)
		out = append(out, in)
	}
	return out, rows.Err()
}

func List(db *sql.DB, status Status) ([]Insight, error) {
	rows, err := db.Query(
		`SELECT id, kind, text, endpoint_a, endpoint_b, conf, model, created, status
		 FROM dream_insights WHERE status = ? ORDER BY conf DESC, created`, string(status))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scan(rows)
}

// Get returns a single insight by id.
func Get(db *sql.DB, id int64) (Insight, error) {
	rows, err := db.Query(
		`SELECT id, kind, text, endpoint_a, endpoint_b, conf, model, created, status
		 FROM dream_insights WHERE id = ?`, id)
	if err != nil {
		return Insight{}, err
	}
	defer rows.Close()
	ins, err := scan(rows)
	if err != nil {
		return Insight{}, err
	}
	if len(ins) == 0 {
		return Insight{}, fmt.Errorf("no insight %d", id)
	}
	return ins[0], nil
}

// SetStatus records the decision. Rejections are kept, not deleted: "you dreamed
// this and I said no" is the signal for tuning what the pass proposes later.
func SetStatus(db *sql.DB, id int64, s Status) error {
	_, err := db.Exec("UPDATE dream_insights SET status = ? WHERE id = ?", string(s), id)
	return err
}

func PendingCount(db *sql.DB) (int, error) {
	var n int
	err := db.QueryRow("SELECT COUNT(*) FROM dream_insights WHERE status = 'pending'").Scan(&n)
	return n, err
}

// Accept turns a reviewed insight into a memory. It enters the store at the
// "dream" confidence (0.5) and a modest salience: a hypothesis that must earn
// belief through corroboration on the ordinary reinforcement path, never one that
// outranks a fact the user stated. The insight itself is marked accepted, not
// deleted, so the decision stays on the record.
func Accept(db *sql.DB, p *provider.Provider, embedModel string, in Insight) (bool, error) {
	if err := in.Validate(); err != nil {
		return false, err
	}
	m := &memory.Memory{
		Text:     in.Text,
		Kind:     memory.Context,
		Salience: 0.4,
		Source:   "dream",
	}
	r, err := memory.Store(db, p, embedModel, m)
	if err != nil {
		return false, err
	}
	if err := SetStatus(db, in.ID, Accepted); err != nil {
		return r.Created(), err
	}
	return r.Created(), nil
}
