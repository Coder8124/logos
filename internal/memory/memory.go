// Package memory is the assistant's persistent memory.
//
// The vault holds what you write down; this holds what the assistant *learns*
// about you across conversations — your preferences, the people you work with,
// standing context and priorities — the things a good human assistant just
// knows and a stateless one forgets the moment a session ends. Memories are
// embedded and recalled semantically, so the relevant ones surface into a later
// conversation without you repeating yourself. That continuity is what turns a
// chatbot into a second brain.
package memory

import (
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/Coder8124/brain/internal/provider"
	"github.com/Coder8124/brain/internal/textmatch"
)

// Kind categorises a memory so it can be weighted and surfaced appropriately.
type Kind string

const (
	// Preference: how the user likes things done ("prefers short emails",
	// "no meetings before 10am"). The heart of the "emotional intelligence" a
	// secretary is expected to have.
	Preference Kind = "preference"
	// Person: a relationship fact ("Sarah is the CFO", "Alex is sensitive
	// about deadlines").
	Person Kind = "person"
	// Fact: a durable fact about the user or their work.
	Fact Kind = "fact"
	// Context: standing situational context ("launching in Q4", "hiring two
	// engineers") — what lets the assistant anticipate.
	Context Kind = "context"
)

// Memory is one remembered thing.
type Memory struct {
	ID       int64   `json:"id"`
	Text     string  `json:"text"`
	Kind     Kind    `json:"kind"`
	Salience float64 `json:"salience"` // 0..1, how much it matters
	// Confidence is how sure we are the fact is true and current, as distinct
	// from salience (how much it matters). A preference you stated by hand is
	// near-certain; one the model inferred from a passing remark is not.
	// Corroboration raises it; being superseded or going stale lowers it.
	Confidence float64 `json:"confidence"` // 0..1
	// Project scopes a memory to one piece of work, so an assistant helping with
	// ÉlyséeBot recalls ÉlyséeBot's context and not the whole life. Empty means
	// global — it applies everywhere.
	Project string `json:"project,omitempty"`
	Source  string `json:"source"` // where it was learned (conversation, manual, mcp)
	// Agent names which coding agent learned this — "claude-code", "cursor",
	// "codex" — when the MCP host said so at handshake. Empty means either the
	// fact predates this field or arrived somewhere that isn't one agent among
	// several (the CLI, a dream pass). Never asked of the model: see
	// internal/mcpserver's clientInfoFromInitialize, which is the only writer.
	Agent        string  `json:"agent,omitempty"`
	Created      int64   `json:"created"`
	LastUsed     int64   `json:"last_used"`
	Uses         int     `json:"uses"`
	SupersededBy int64   `json:"superseded_by,omitempty"` // id of the memory that replaced this one
	Score        float64 `json:"score"`                   // set on recall: relevance to the query
	vec          []byte  // embedding, loaded only for consolidation; not serialised
}

const Schema = `
CREATE TABLE IF NOT EXISTS memories (
    id            INTEGER PRIMARY KEY,
    text          TEXT NOT NULL,
    kind          TEXT NOT NULL,
    salience      REAL NOT NULL DEFAULT 0.5,
    confidence    REAL NOT NULL DEFAULT 0.7,
    project       TEXT NOT NULL DEFAULT '',
    source        TEXT,
    agent         TEXT NOT NULL DEFAULT '',
    created       INTEGER NOT NULL,
    last_used     INTEGER NOT NULL DEFAULT 0,
    uses          INTEGER NOT NULL DEFAULT 0,
    vec           BLOB,
    fingerprint   TEXT UNIQUE,
    superseded    INTEGER NOT NULL DEFAULT 0,
    superseded_by INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS memories_kind ON memories(kind);
CREATE INDEX IF NOT EXISTS memories_project ON memories(project);

-- memory_log is the git history of memory: an append-only record of every
-- lifecycle event (created, reinforced, superseded, merged, forgotten). The
-- detail column snapshots the memory's text at the time, so the history stays
-- legible even after the memory itself is deleted.
CREATE TABLE IF NOT EXISTS memory_log (
    id     INTEGER PRIMARY KEY,
    ts     INTEGER NOT NULL,
    mem_id INTEGER NOT NULL,
    event  TEXT NOT NULL,
    detail TEXT,
    ref_id INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS memory_log_ts ON memory_log(ts);
`

func Init(db *sql.DB) error {
	if _, err := db.Exec(Schema); err != nil {
		return err
	}
	// Migrations for stores created before these columns existed. Each is a
	// no-op once applied; errors (column already present) are ignored.
	db.Exec("ALTER TABLE memories ADD COLUMN superseded INTEGER NOT NULL DEFAULT 0")
	db.Exec("ALTER TABLE memories ADD COLUMN confidence REAL NOT NULL DEFAULT 0.7")
	db.Exec("ALTER TABLE memories ADD COLUMN project TEXT NOT NULL DEFAULT ''")
	db.Exec("ALTER TABLE memories ADD COLUMN superseded_by INTEGER NOT NULL DEFAULT 0")
	db.Exec("ALTER TABLE memories ADD COLUMN agent TEXT NOT NULL DEFAULT ''")
	return nil
}

func fingerprint(text string) string {
	return strings.ToLower(strings.Join(strings.Fields(text), " "))
}

// A Receipt says what actually happened to a memory that was stored.
//
// "Saved" is not one outcome. The same call can create a memory, corroborate
// one that already existed, or do nothing — and a caller that cannot tell those
// apart will report all three as success. That matters most across MCP, where
// an agent tells the user it remembered something: it should be able to say
// whether it learned a new fact or confirmed an old one.
type Receipt struct {
	Outcome string `json:"outcome"` // EvCreated, EvReinforced, or OutcomeNoop
	ID      int64  `json:"id"`
	// Ref is the memory that was reinforced, when the store folded into an
	// existing one instead of adding a twin.
	Ref int64 `json:"ref,omitempty"`
}

// OutcomeNoop means nothing was stored — there was nothing to store.
const OutcomeNoop = "noop"

// Created reports whether the store added a new memory, which is what most
// callers actually want to branch on.
func (r Receipt) Created() bool { return r.Outcome == EvCreated }

// Store embeds a memory and saves it, deduplicated on its normalised text so
// the same fact learned twice does not accumulate. The receipt distinguishes a
// new memory from a corroborated one.
func Store(db *sql.DB, p *provider.Provider, embedModel string, m *Memory) (Receipt, error) {
	if strings.TrimSpace(m.Text) == "" {
		return Receipt{Outcome: OutcomeNoop}, nil
	}
	if m.Created == 0 {
		m.Created = time.Now().Unix()
	}
	if m.Salience == 0 {
		m.Salience = 0.5
	}
	if m.Confidence == 0 {
		m.Confidence = defaultConfidence(m.Source)
	}

	var vec []byte
	var qvec []float32
	if p != nil {
		vecs, err := p.Embed(embedModel, []string{m.Text})
		if err == nil && len(vecs) == 1 {
			qvec = vecs[0]
			vec = floatsToBlob(qvec)
		}
	}

	// Semantic dedup: the model paraphrases the same fact differently every time
	// it re-extracts it, so exact-text dedup lets re-learning bloat the store.
	// If an existing memory is near-identical in meaning, reinforce it instead of
	// adding a twin — this is what keeps the store from growing on repetition.
	// A re-statement is corroboration, so it also lifts confidence.
	if qvec != nil {
		if id, ok := nearestMemory(db, qvec, m.Text, DedupThreshold); ok {
			db.Exec("UPDATE memories SET salience = MIN(1.0, salience + 0.05), confidence = MIN(1.0, confidence + 0.05), uses = uses + 1 WHERE id = ?", id)
			logEvent(db, id, EvReinforced, m.Text, 0)
			return Receipt{Outcome: EvReinforced, ID: id, Ref: id}, flush(db, m.Kind)
		}
	}

	res, err := db.Exec(
		`INSERT OR IGNORE INTO memories (text, kind, salience, confidence, project, source, agent, created, vec, fingerprint)
		 VALUES (?,?,?,?,?,?,?,?,?,?)`,
		m.Text, string(m.Kind), m.Salience, m.Confidence, m.Project, m.Source, m.Agent, m.Created, vec, fingerprint(m.Text))
	if err != nil {
		return Receipt{}, err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		m.ID, _ = res.LastInsertId()
		logEvent(db, m.ID, EvCreated, m.Text, 0)
		return Receipt{Outcome: EvCreated, ID: m.ID}, flush(db, m.Kind)
	}

	// The fingerprint already existed — the same sentence, word for word. That
	// is corroboration too, and reporting it as a silent no-op is how a caller
	// ends up telling the user it saved something that was already there.
	var id int64
	if db.QueryRow("SELECT id FROM memories WHERE fingerprint = ?", fingerprint(m.Text)).Scan(&id) == nil && id > 0 {
		db.Exec("UPDATE memories SET uses = uses + 1 WHERE id = ?", id)
		logEvent(db, id, EvReinforced, m.Text, 0)
		m.ID = id
		return Receipt{Outcome: EvReinforced, ID: id, Ref: id}, nil
	}
	return Receipt{Outcome: OutcomeNoop}, nil
}

// defaultConfidence seeds a memory's confidence from how it was learned. A fact
// the user typed or a tool asserted is near-certain; one inferred from the drift
// of a conversation is a hypothesis that corroboration must earn up.
func defaultConfidence(source string) float64 {
	switch source {
	case "manual":
		return 0.9
	case "mcp":
		return 0.85
	case "conversation", "":
		return 0.6
	case "dream":
		// Synthesised offline by the nightly consolidation pass — a hypothesis,
		// not something the user said. It must earn belief through corroboration
		// and never outranks a stated fact at equal relevance.
		return 0.5
	default:
		return 0.7
	}
}

// DedupThreshold is how close a new memory must be to an existing one to be
// treated as the same fact. High, so genuinely distinct facts about the same
// subject are kept; only near-restatements collapse.
//
// The threshold alone was not enough — records that share a sentence frame and
// differ only in a number sit above it. It is now one of two conditions, the
// other being sameFact.
const DedupThreshold = 0.87

// nearestMemory returns the id of the most-similar active memory if it clears
// the threshold — the write-time guard against near-duplicate accumulation.
//
// text is the statement being stored, and it is not optional: a candidate that
// asserts a different value is rejected however close its vector is. See
// sameFact for why. Candidates are considered in similarity order, so a genuine
// restatement sitting just behind a value-differing twin is still found.
func nearestMemory(db *sql.DB, query []float32, text string, threshold float64) (int64, bool) {
	rows, err := db.Query(`SELECT id, text, vec FROM memories WHERE superseded = 0 AND vec IS NOT NULL`)
	if err != nil {
		return 0, false
	}
	defer rows.Close()
	var bestID int64
	best := threshold
	for rows.Next() {
		var id int64
		var candidate string
		var vec []byte
		if rows.Scan(&id, &candidate, &vec) != nil || len(vec) == 0 {
			continue
		}
		sim := cosine(query, blobToFloats(vec))
		if sim < best || !sameFact(text, candidate) {
			continue
		}
		best = sim
		bestID = id
	}
	return bestID, bestID != 0
}

// sameFact is the second opinion the embedding cannot give.
//
// Cosine similarity is a judgement about topic, and two records in the same
// sentence frame are the same topic by construction. Measured against
// nomic-embed-text: "the server in rack 12 is running hot" and "…rack 13…"
// score 0.958; "$38" against "$42" scores 0.978. Both are well past any dedup
// threshold, and both are two facts rather than one said twice. The digits are
// the whole content of the difference and are close to invisible to the model.
//
// So a merge additionally requires that the two statements do not assert
// different values. The asymmetry is the point: a duplicate that survives costs
// a line in a file the user can delete, and a fact merged away is gone for good
// — the text is never written, so neither the vault copy nor the log can bring
// it back.
func sameFact(incoming, existing string) bool {
	return !textmatch.DifferingValues(incoming, existing)
}

// Recall returns the memories most relevant to a query, by embedding similarity.
// The used memories have their reinforcement bumped, so what proves useful
// stays fresh — memory that is exercised persists, memory that never helps fades
// in the ranking.
func Recall(db *sql.DB, p *provider.Provider, embedModel, query string, k int) ([]Memory, error) {
	if p == nil {
		return All(db) // no embedding backend: fall back to everything, salience-first
	}
	vecs, err := p.Embed(embedModel, []string{query})
	if err != nil || len(vecs) == 0 {
		return All(db)
	}
	mems, err := recallByVec(db, vecs[0], k, query)
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	for _, m := range mems {
		db.Exec("UPDATE memories SET last_used = ?, uses = uses + 1 WHERE id = ?", now, m.ID)
	}
	return mems, nil
}

// recallByVec ranks stored memories using hybrid retrieval (vector + BM25 fused)
// blended with effective salience. queryText drives the lexical arm; pass "" to
// fall back to pure vector. The +8.9-point lift this gave on LongMemEval is why
// live recall runs it too, not just the benchmark.
func recallByVec(db *sql.DB, query []float32, k int, queryText string) ([]Memory, error) {
	return recallScoped(db, query, k, queryText, "")
}

// recallScoped is recallByVec with an optional project filter. An empty project
// recalls everything (global view); a named project narrows to that work's own
// memory plus global memories, which is what project-scoped recall wants.
func recallScoped(db *sql.DB, query []float32, k int, queryText, project string) ([]Memory, error) {
	q := `SELECT id, text, kind, salience, confidence, project, source, agent, created, last_used, uses, vec FROM memories WHERE superseded = 0`
	var args []any
	if project != "" {
		q += " AND (project = ? OR project = '')"
		args = append(args, project)
	}
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var mems []Memory
	var cands []Candidate
	for rows.Next() {
		var m Memory
		var kind string
		var vec []byte
		if err := rows.Scan(&m.ID, &m.Text, &kind, &m.Salience, &m.Confidence, &m.Project, &m.Source, &m.Agent, &m.Created, &m.LastUsed, &m.Uses, &vec); err != nil {
			return nil, err
		}
		m.Kind = Kind(kind)
		if len(vec) == 0 {
			continue
		}
		mems = append(mems, m)
		cands = append(cands, Candidate{ID: fmt.Sprint(m.ID), Text: m.Text, Vec: blobToFloats(vec)})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	fused := Fuse(queryText, query, cands) // normalised 0..1 relevance per candidate
	now := time.Now().Unix()
	for i := range mems {
		rel := 0.0
		if i < len(fused) {
			rel = fused[i]
		}
		// Relevance dominates; effective salience (decay + reinforcement) breaks
		// ties toward what currently matters, scaled by how much we trust the
		// memory so a shaky fact does not outrank a certain one at equal relevance.
		mems[i].Score = rel*0.85 + EffectiveSalience(mems[i], now)*mems[i].Confidence*0.15
	}
	sortByScore(mems)
	if len(mems) > k {
		mems = mems[:k]
	}
	return mems, nil
}

// RecallInProject is Recall narrowed to a project's memory (plus global memory).
func RecallInProject(db *sql.DB, p *provider.Provider, embedModel, query, project string, k int) ([]Memory, error) {
	// The no-provider fallback must still honour the project, or scoping is a
	// no-op on every machine without a model runtime — which since the MCP
	// server learned to start without one is a supported configuration, not an
	// edge case. Losing relevance ranking is acceptable here; leaking another
	// project's facts is not.
	if p == nil {
		return AllInProject(db, project)
	}
	vecs, err := p.Embed(embedModel, []string{query})
	if err != nil || len(vecs) == 0 {
		return AllInProject(db, project)
	}
	mems, err := recallScoped(db, vecs[0], k, query, project)
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	for _, m := range mems {
		db.Exec("UPDATE memories SET last_used = ?, uses = uses + 1 WHERE id = ?", now, m.ID)
	}
	return mems, nil
}

// All returns every active memory, most salient first — the fallback and the
// CLI view. Superseded memories are excluded (they are history, not truth).
func All(db *sql.DB) ([]Memory, error) {
	rows, err := db.Query(
		`SELECT id, text, kind, salience, confidence, project, source, agent, created, last_used, uses, superseded_by FROM memories WHERE superseded = 0 ORDER BY salience DESC, created DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Memory
	for rows.Next() {
		var m Memory
		var kind string
		if err := rows.Scan(&m.ID, &m.Text, &kind, &m.Salience, &m.Confidence, &m.Project, &m.Source, &m.Agent, &m.Created, &m.LastUsed, &m.Uses, &m.SupersededBy); err != nil {
			return nil, err
		}
		m.Kind = Kind(kind)
		out = append(out, m)
	}
	return out, rows.Err()
}

// AllInProject is All narrowed to one project's memories plus the global ones,
// which is what a project-scoped view wants: the work's own facts, and the
// standing truths that apply to every project. An empty project means no
// narrowing at all, so this degrades to All.
func AllInProject(db *sql.DB, project string) ([]Memory, error) {
	if strings.TrimSpace(project) == "" {
		return All(db)
	}
	rows, err := db.Query(
		`SELECT id, text, kind, salience, confidence, project, source, agent, created, last_used, uses, superseded_by
		   FROM memories
		  WHERE superseded = 0 AND (project = ? OR project = '')
		  ORDER BY salience DESC, created DESC`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Memory
	for rows.Next() {
		var m Memory
		var kind string
		if err := rows.Scan(&m.ID, &m.Text, &kind, &m.Salience, &m.Confidence, &m.Project, &m.Source, &m.Agent, &m.Created, &m.LastUsed, &m.Uses, &m.SupersededBy); err != nil {
			return nil, err
		}
		m.Kind = Kind(kind)
		out = append(out, m)
	}
	return out, rows.Err()
}

func Count(db *sql.DB) (int, error) {
	var n int
	err := db.QueryRow("SELECT COUNT(*) FROM memories").Scan(&n)
	return n, err
}

func Forget(db *sql.DB, id int64) error {
	// Snapshot the text into the log first, so the timeline records what was
	// forgotten even though the row is about to vanish.
	var text, kind string
	db.QueryRow("SELECT text, kind FROM memories WHERE id = ?", id).Scan(&text, &kind)
	_, err := db.Exec("DELETE FROM memories WHERE id = ?", id)
	if err != nil {
		return err
	}
	logEvent(db, id, EvForgotten, text, 0)
	// Forgetting has to reach the file too, or the next import restores it —
	// which makes a failure here worth reporting rather than swallowing.
	return flush(db, Kind(kind))
}

// ForgetBySource removes every memory learned a particular way and reports how
// many went. It returns the count rather than nothing so a caller can tell "I
// removed forty" from "there was nothing there".
//
// This is what makes a bulk seeding reversible. `brain bootstrap` writes several
// memories at once from git history, and a user who dislikes the result should
// not have to forget them one id at a time — nor should they have to guess which
// ids were the machine's and which were theirs. Source is the record of how a
// fact arrived, so it is the honest handle for undoing a whole arrival.
//
// Each row goes through Forget, so the timeline records every removal
// individually and the vault files are flushed. A bulk DELETE would be faster
// and would leave the vault holding notes the database no longer knows about.
func ForgetBySource(db *sql.DB, source string) (int, error) {
	if strings.TrimSpace(source) == "" {
		return 0, fmt.Errorf("no source given")
	}
	rows, err := db.Query("SELECT id FROM memories WHERE source = ?", source)
	if err != nil {
		return 0, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	var n int
	for _, id := range ids {
		if err := Forget(db, id); err != nil {
			// Report what was already removed alongside the failure: a caller
			// that retries needs to know the operation was partial.
			return n, err
		}
		n++
	}
	return n, nil
}

// --- vector helpers (shared shape with the index) ---

func cosine(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		x, y := float64(a[i]), float64(b[i])
		dot += x * y
		na += x * x
		nb += y * y
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

func floatsToBlob(v []float32) []byte {
	b := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(b[4*i:], math.Float32bits(f))
	}
	return b
}

func blobToFloats(b []byte) []float32 {
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:]))
	}
	return v
}

func sortByScore(m []Memory) {
	for i := 1; i < len(m); i++ {
		for j := i; j > 0 && m[j].Score > m[j-1].Score; j-- {
			m[j], m[j-1] = m[j-1], m[j]
		}
	}
}
