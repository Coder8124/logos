package rollup

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"time"
)

// Kind of change a proposal would make to the vault.
type Kind string

const (
	NewNote Kind = "new_note"
	Append  Kind = "append"
	NewEdge Kind = "new_edge"
	Merge   Kind = "merge"
)

type Status string

const (
	Pending  Status = "pending"
	Accepted Status = "accepted"
	Rejected Status = "rejected"
)

// Proposal is a change the system would like to make, and never makes on its
// own until the user raises the auto-accept threshold.
//
// Evidence is mandatory and enforced at write time. A proposal that cannot be
// traced back to observed events is a hallucination with good manners, and it
// must be impossible to accept one.
type Proposal struct {
	ID       int64
	Kind     Kind
	Target   string // note slug
	Payload  Payload
	Conf     float64
	Evidence []int64 // event ids
	Model    string
	Created  int64
	Status   Status
}

// Payload carries the kind-specific detail. One struct rather than four keeps
// the queue table and the review UI simple; unused fields stay empty.
type Payload struct {
	Title string `json:"title,omitempty"`
	Type  string `json:"type,omitempty"`
	Body  string `json:"body,omitempty"`
	// Edge fields.
	Pred string `json:"pred,omitempty"`
	Obj  string `json:"obj,omitempty"`
	// Merge field: the slug to fold Target into.
	Into string `json:"into,omitempty"`
}

const QueueSchema = `
CREATE TABLE IF NOT EXISTS proposals (
    id       INTEGER PRIMARY KEY,
    kind     TEXT NOT NULL,
    target   TEXT NOT NULL,
    payload  TEXT NOT NULL,
    conf     REAL NOT NULL,
    evidence TEXT NOT NULL,
    model    TEXT NOT NULL,
    created  INTEGER NOT NULL,
    status   TEXT NOT NULL DEFAULT 'pending'
);
CREATE INDEX IF NOT EXISTS proposals_status ON proposals(status, created);
`

func InitQueue(db *sql.DB) error {
	_, err := db.Exec(QueueSchema)
	return err
}

// Validate enforces the invariants that make the queue trustworthy. Called on
// every insert rather than trusted from the model.
//
// Every field here is model output, written by a small local model reading
// captured text — web pages, other people's messages, whatever was on the
// clipboard. So the target is checked for staying inside the vault (joining it
// on unchecked made accepting a proposal a way to write a file anywhere the
// user can write) and the one-line fields are checked for the newlines that
// would let a note's frontmatter be forged from its own title.
func (p *Proposal) Validate() error {
	if len(p.Evidence) == 0 {
		return fmt.Errorf("proposal for %q has no evidence — refusing to queue it", p.Target)
	}
	if strings.TrimSpace(p.Target) == "" {
		return fmt.Errorf("proposal has no target")
	}
	if err := checkTarget(p.Target); err != nil {
		return err
	}
	if p.Conf < 0 || p.Conf > 1 {
		return fmt.Errorf("proposal for %q has confidence %v outside 0..1", p.Target, p.Conf)
	}
	for name, v := range map[string]string{
		"title": p.Payload.Title, "type": p.Payload.Type,
		"pred": p.Payload.Pred, "obj": p.Payload.Obj,
	} {
		if strings.ContainsAny(v, "\n\r") {
			return fmt.Errorf("proposal for %q has a line break in its %s, which no name has",
				p.Target, name)
		}
	}
	if p.Payload.Into != "" {
		if err := checkTarget(p.Payload.Into); err != nil {
			return err
		}
	}
	switch p.Kind {
	case NewNote, Append:
		if strings.TrimSpace(p.Payload.Body) == "" {
			return fmt.Errorf("%s proposal for %q has an empty body", p.Kind, p.Target)
		}
	case NewEdge:
		if p.Payload.Pred == "" || p.Payload.Obj == "" {
			return fmt.Errorf("edge proposal for %q is missing pred or obj", p.Target)
		}
	case Merge:
		if p.Payload.Into == "" {
			return fmt.Errorf("merge proposal for %q has no target to merge into", p.Target)
		}
	default:
		return fmt.Errorf("unknown proposal kind %q", p.Kind)
	}
	return nil
}

// checkTarget refuses a note slug that would not land inside the vault.
//
// Checked here as well as at write time on purpose: a proposal that could never
// be safely applied has no business sitting in the review queue looking like a
// decision the user gets to make.
func checkTarget(slug string) error {
	clean := path.Clean("/" + strings.ReplaceAll(strings.TrimSpace(slug), `\`, "/"))
	if clean == "/" {
		return fmt.Errorf("proposal target %q names no note", slug)
	}
	// path.Clean on a rooted path resolves every ".." it can; what is left after
	// trimming the root must be the slug itself, or the slug was reaching out of
	// the vault (or in by an absolute path, which is the same escape spelled
	// differently).
	if strings.TrimPrefix(clean, "/") != strings.TrimSpace(slug) {
		return fmt.Errorf("proposal target %q would write outside the vault", slug)
	}
	return nil
}

func Enqueue(db *sql.DB, p *Proposal) error {
	if err := p.Validate(); err != nil {
		return err
	}
	payload, err := json.Marshal(p.Payload)
	if err != nil {
		return err
	}
	evidence, err := json.Marshal(p.Evidence)
	if err != nil {
		return err
	}
	if p.Created == 0 {
		p.Created = time.Now().Unix()
	}
	if p.Status == "" {
		p.Status = Pending
	}

	res, err := db.Exec(
		`INSERT INTO proposals (kind, target, payload, conf, evidence, model, created, status)
		 VALUES (?,?,?,?,?,?,?,?)`,
		string(p.Kind), p.Target, string(payload), p.Conf, string(evidence), p.Model, p.Created, string(p.Status))
	if err != nil {
		return err
	}
	p.ID, _ = res.LastInsertId()
	return nil
}

func List(db *sql.DB, status Status) ([]Proposal, error) {
	rows, err := db.Query(
		`SELECT id, kind, target, payload, conf, evidence, model, created, status
		 FROM proposals WHERE status = ? ORDER BY conf DESC, created`, string(status))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Proposal
	for rows.Next() {
		var p Proposal
		var kind, payload, evidence, st string
		if err := rows.Scan(&p.ID, &kind, &p.Target, &payload, &p.Conf, &evidence, &p.Model, &p.Created, &st); err != nil {
			return nil, err
		}
		p.Kind, p.Status = Kind(kind), Status(st)
		json.Unmarshal([]byte(payload), &p.Payload)
		json.Unmarshal([]byte(evidence), &p.Evidence)
		out = append(out, p)
	}
	return out, rows.Err()
}

// SetStatus records the decision. Rejections are kept, not deleted: "you
// proposed this and I said no" is the signal for tuning the auto-accept
// threshold later.
func SetStatus(db *sql.DB, id int64, s Status) error {
	_, err := db.Exec("UPDATE proposals SET status = ? WHERE id = ?", string(s), id)
	return err
}

func PendingCount(db *sql.DB) (int, error) {
	var n int
	err := db.QueryRow("SELECT COUNT(*) FROM proposals WHERE status = 'pending'").Scan(&n)
	return n, err
}

// Summary is the one-line form shown in the review queue.
func (p Proposal) Summary() string {
	switch p.Kind {
	case NewNote:
		return fmt.Sprintf("create %s (%s) — %q", p.Target, p.Payload.Type, firstLine(p.Payload.Body))
	case Append:
		return fmt.Sprintf("append to %s — %q", p.Target, firstLine(p.Payload.Body))
	case NewEdge:
		return fmt.Sprintf("%s —%s→ %s", p.Target, p.Payload.Pred, p.Payload.Obj)
	case Merge:
		return fmt.Sprintf("merge %s into %s", p.Target, p.Payload.Into)
	}
	return string(p.Kind)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 60 {
		s = s[:60] + "…"
	}
	return s
}
