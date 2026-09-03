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
	Agent        string `json:"agent,omitempty"`
	Created      int64  `json:"created"`
	LastUsed     int64  `json:"last_used"`
	Uses         int    `json:"uses"`
	SupersededBy int64  `json:"superseded_by,omitempty"` // id of the memory that replaced this one
	// Quarantined means a machine proposed this and nobody has looked at it yet.
	// A quarantined memory is a real row — same table, same shape — it is just
	// invisible to Recall/All until a human accepts it. See quarantine.go.
	Quarantined bool    `json:"quarantined,omitempty"`
	Score       float64 `json:"score"` // set on recall: relevance to the query
	// Pin overrides ranking entirely: PinAlways forces inclusion in a context
	// pack regardless of relevance score, PinNever removes a memory from
	// recall while leaving it on record. See PinAlways and PinNever.
	Pin int    `json:"pin,omitempty"`
	vec []byte // embedding, loaded only for consolidation; not serialised
}

// Pin states. PinNone is the default: ranked normally, neither forced in nor
// held out. Deliberately two states rather than a bool, because "always show
// me this" and "never show me this" are different user intents, not opposites
// of the same one — a memory can need overriding in either direction, and a
// single flag can only say one of them.
const (
	PinNone   = 0 // ranked normally
	PinAlways = 1 // always included in a context pack, budget permitting — see spendMemories
	PinNever  = 2 // excluded from recall entirely; still on record, still listed by All
)

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
    superseded_by INTEGER NOT NULL DEFAULT 0,
    quarantined   INTEGER NOT NULL DEFAULT 0,
    pin           INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS memories_kind ON memories(kind);

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
    ref_id INTEGER NOT NULL DEFAULT 0,
    -- The project the memory belonged to, copied onto the line rather than
    -- joined to at read time. A join would be tidier and would also lose every
    -- forgotten memory, because forgetting deletes the row there is nothing
    -- left to join to — and the events a person most wants out of a project
    -- timeline are exactly the ones where something went away.
    project TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS memory_log_ts ON memory_log(ts);
`

func Init(db *sql.DB) error {
	if _, err := db.Exec(Schema); err != nil {
		return err
	}
	// Migrations for stores created before these columns existed. Each is a
	// no-op once applied; errors (column already present) are ignored.
	//
	// Every index over a column added here lives *below* its ALTER rather than
	// in Schema. CREATE TABLE IF NOT EXISTS is a no-op against a table that
	// already exists, so a Schema that indexes a later-added column indexes a
	// column an existing vault does not have yet — and that one hard error
	// aborts Init before any migration runs, which turns an additive change
	// into a store that will not open at all.
	db.Exec("ALTER TABLE memories ADD COLUMN superseded INTEGER NOT NULL DEFAULT 0")
	db.Exec("ALTER TABLE memories ADD COLUMN confidence REAL NOT NULL DEFAULT 0.7")
	db.Exec("ALTER TABLE memories ADD COLUMN project TEXT NOT NULL DEFAULT ''")
	db.Exec("CREATE INDEX IF NOT EXISTS memories_project ON memories(project)")
	db.Exec("ALTER TABLE memories ADD COLUMN superseded_by INTEGER NOT NULL DEFAULT 0")
	db.Exec("ALTER TABLE memories ADD COLUMN agent TEXT NOT NULL DEFAULT ''")
	db.Exec("ALTER TABLE memories ADD COLUMN pin INTEGER NOT NULL DEFAULT 0")
	// Stage 4: quarantine. Defaulting existing rows to 0 (not quarantined) is
	// what makes this additive — every memory anyone already has stays exactly
	// as visible and recallable as it was before this column existed.
	db.Exec("ALTER TABLE memories ADD COLUMN quarantined INTEGER NOT NULL DEFAULT 0")
	db.Exec("CREATE INDEX IF NOT EXISTS memories_quarantined ON memories(quarantined)")
	db.Exec("ALTER TABLE memory_log ADD COLUMN project TEXT NOT NULL DEFAULT ''")
	db.Exec("CREATE INDEX IF NOT EXISTS memory_log_project ON memory_log(project, ts)")
	// Backfill what is still knowable. A log line written before the column
	// existed can be attributed if its memory is still there; one whose memory
	// has since been forgotten cannot, and stays blank rather than being
	// guessed at. That is the honest half-answer: an old timeline is partial,
	// and TimelineInProject says so rather than silently presenting it as whole.
	db.Exec(`UPDATE memory_log SET project = (
	             SELECT m.project FROM memories m WHERE m.id = memory_log.mem_id)
	         WHERE project = ''
	           AND EXISTS (SELECT 1 FROM memories m WHERE m.id = memory_log.mem_id)`)
	return nil
}

func fingerprint(text string) string {
	return strings.ToLower(strings.Join(strings.Fields(text), " "))
}

// A Receipt says what actually happened to a memory that was stored.
//
// "Saved" is not one outcome. The same call can create a memory, corroborate
// one that already existed, land in quarantine awaiting review, or do nothing
// — and a caller that cannot tell those apart will report all four as success.
// That matters most across MCP, where an agent tells the user it remembered
// something: it should be able to say whether it learned a new fact, confirmed
// an old one, or just queued something for the user to look at.
type Receipt struct {
	Outcome string `json:"outcome"` // EvCreated, EvReinforced, EvQuarantined, or OutcomeNoop
	ID      int64  `json:"id"`
	// Ref is the memory that was reinforced, when the store folded into an
	// existing one instead of adding a twin.
	Ref int64 `json:"ref,omitempty"`
}

// OutcomeNoop means nothing was stored — there was nothing to store.
const OutcomeNoop = "noop"

// Created reports whether the store added a new, immediately-active memory,
// which is what most callers actually want to branch on. A quarantined
// arrival is deliberately excluded — it exists in the store, but nothing may
// treat it as learned until a human accepts it.
func (r Receipt) Created() bool { return r.Outcome == EvCreated }

// Queued reports whether the store landed in quarantine rather than going
// active — the signal a caller uses to tell the user "queued for review"
// instead of "remembered".
func (r Receipt) Queued() bool { return r.Outcome == EvQuarantined }

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

	// Adopt anything the user changed in the file by hand before this write
	// regenerates it. Ordered before the insert, not after: once the new row is
	// in the index, nothing can tell it apart from a line the user deleted.
	// A failure here is not fatal to the store — see Reconcile — but it is
	// returned, because writing over an edit the user made is exactly what this
	// call exists to prevent and doing it anyway would be worse than refusing.
	if err := Reconcile(db, m.Kind); err != nil {
		return Receipt{Outcome: OutcomeNoop}, err
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

	dup, err := insertMemory(db, m, vec)
	if err != nil {
		return Receipt{}, err
	}

	// The fingerprint already existed — the same sentence, word for word. That
	// is corroboration too, and reporting it as a silent no-op is how a caller
	// ends up telling the user it saved something that was already there.
	if dup > 0 {
		db.Exec("UPDATE memories SET uses = uses + 1 WHERE id = ?", dup)
		logEvent(db, dup, EvReinforced, m.Text, 0)
		m.ID = dup
		return Receipt{Outcome: EvReinforced, ID: dup, Ref: dup}, nil
	}

	if m.Quarantined {
		// Quarantined arrivals get their own event, not EvCreated — the
		// timeline should be able to say "proposed" and "accepted" as two
		// distinct moments, the same way it already distinguishes created
		// from reinforced. See Accept in quarantine.go for the other half.
		logEvent(db, m.ID, EvQuarantined, m.Text, 0)
		// A proposal is written down too. Not into memories/<kind>.md —
		// that would defeat quarantine — but into the queue file, so a
		// review the user has not got to yet survives deleting the cache.
		// See internal/memory/pendingstore.go.
		return Receipt{Outcome: EvQuarantined, ID: m.ID}, flushPending(db)
	}
	logEvent(db, m.ID, EvCreated, m.Text, 0)
	return Receipt{Outcome: EvCreated, ID: m.ID}, flush(db, m.Kind)
}

// idAttempts bounds the retry below. Eight writers colliding on one number is
// already an unusual amount of concurrency for a personal vault; sixteen rounds
// of losing that race is a symptom, not a case to keep quietly retrying.
const idAttempts = 16

// insertMemory allocates an id and writes the row. It returns the id of an
// existing memory when the same sentence is already stored, and sets m.ID when
// it created one.
//
// This exists because "allocate then insert" was two statements with nothing
// between them, and nextID is a MAX(id) read. Two agents calling `remember` at
// the same moment — which is the ordinary case for this product, since Claude
// Code and Cursor each run their own MCP server against one vault — both read
// the same maximum and both tried to insert the same number. INSERT OR IGNORE
// swallowed the primary key conflict, RowsAffected came back 0, the fingerprint
// lookup below found nothing because the two memories said different things,
// and Store returned Receipt{Outcome: OutcomeNoop} with a nil error: the memory
// was dropped and the agent was told the call succeeded. Eight concurrent
// remembers left two memories.
//
// The fix is to notice losing the race and take another number. A conflict on
// the id is retryable — someone else got there first, the next MAX is higher.
// A conflict on the fingerprint is not: it means this exact text is already
// stored, which is the corroboration path the caller wants told apart from a
// fresh write. Those two are distinguished by asking who owns the fingerprint,
// because INSERT OR IGNORE cannot say which constraint it ignored.
//
// Exhausting the attempts is an error rather than a silent no-op. A memory the
// system could not store is a thing the user must hear about; the whole failure
// this replaces was that it looked like success.
func insertMemory(db *sql.DB, m *Memory, vec []byte) (int64, error) {
	fp := fingerprint(m.Text)
	for range idAttempts {
		// The id is chosen rather than left to the rowid counter, so a forgotten
		// memory's number is never handed to an unrelated one. See nextID.
		id := nextID(db)
		res, err := db.Exec(
			`INSERT OR IGNORE INTO memories (id, text, kind, salience, confidence, project, source, agent, created, vec, fingerprint, quarantined)
			 VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
			id, m.Text, string(m.Kind), m.Salience, m.Confidence, m.Project, m.Source,
			m.Agent, m.Created, vec, fp, boolToInt(m.Quarantined))
		if err != nil {
			return 0, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			m.ID = id
			return 0, nil
		}
		var owner int64
		if db.QueryRow("SELECT id FROM memories WHERE fingerprint = ?", fp).Scan(&owner) == nil && owner > 0 {
			return owner, nil
		}
		// Nobody owns the fingerprint, so what was rejected was the id: another
		// writer took it between the MAX read and the insert. Go round again.
	}
	return 0, fmt.Errorf("could not store %q: %d id collisions in a row, which means something else is writing to this vault at the same time",
		oneLine(m.Text), idAttempts)
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
//
// Deliberately not filtered on quarantined: a fresh arrival that restates an
// already-accepted memory should corroborate it, same as always, and an
// arrival that restates something still sitting in the queue should reinforce
// that pending entry rather than queuing a second copy of the same proposal.
// Either way the review queue stays one line per fact instead of growing every
// time an agent repeats itself.
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
//
// That reasoning was applied to numbers and stopped there, which left the same
// hole open for every other kind of distinguishing word. Twenty facts of the
// form "kestrel handles <area> through a dedicated service" collapse to five,
// and each destroyed one reports "already knew that — reinforced memory #1"
// about a memory concerning a different area entirely. A silent loss is bad; a
// silent loss with a confident receipt denying it is worse, because it is the
// receipt that stops the user checking.
//
// So the second condition is now the general one: when each statement names
// something the other does not, they are two facts whatever their vectors say.
// A restatement that merely adds or drops words still collapses, because one
// side's subject is contained in the other's — see DifferentSubjects.
func sameFact(incoming, existing string) bool {
	if textmatch.DifferingValues(incoming, existing) {
		return false
	}
	return !textmatch.DifferentSubjects(incoming, existing)
}

// Recall returns the memories most relevant to a query, by embedding similarity.
// The used memories have their reinforcement bumped, so what proves useful
// stays fresh — memory that is exercised persists, memory that never helps fades
// in the ranking.
func Recall(db *sql.DB, p *provider.Provider, embedModel, query string, k int) ([]Memory, error) {
	if p == nil {
		mems, err := All(db) // no embedding backend: fall back to everything, salience-first
		return excludeNever(mems), err
	}
	vecs, err := p.Embed(embedModel, []string{query})
	if err != nil || len(vecs) == 0 {
		mems, err := All(db)
		return excludeNever(mems), err
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
	// quarantined = 0 is the enforcement point for "a quarantined memory must
	// not be returned by normal recall" — every read path funnels through here
	// or through All/AllInProject below, so this one line is what makes the
	// guarantee real rather than aspirational.
	//
	// PinNever is excluded in the same clause and for the same reason, rather
	// than after ranking: it is what "never include" is for, and a memory that
	// never reaches the candidate list cannot leak in via a generous k or a
	// scoring quirk.
	q := `SELECT id, text, kind, salience, confidence, project, source, agent, created, last_used, uses, vec, pin FROM memories WHERE superseded = 0 AND quarantined = 0 AND pin != ?`
	args := []any{PinNever}
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
		if err := rows.Scan(&m.ID, &m.Text, &kind, &m.Salience, &m.Confidence, &m.Project, &m.Source, &m.Agent, &m.Created, &m.LastUsed, &m.Uses, &vec, &m.Pin); err != nil {
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
	// A pinned memory belongs in the pack regardless of where it lands here —
	// pack assembly asks for it separately (see contextpack's use of Pinned)
	// rather than depending on it surviving this cutoff, so no special case is
	// needed in this function for PinAlways.
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
		mems, err := AllInProject(db, project)
		return excludeNever(mems), err
	}
	vecs, err := p.Embed(embedModel, []string{query})
	if err != nil || len(vecs) == 0 {
		mems, err := AllInProject(db, project)
		return excludeNever(mems), err
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
		`SELECT id, text, kind, salience, confidence, project, source, agent, created, last_used, uses, superseded_by, pin FROM memories WHERE superseded = 0 AND quarantined = 0 ORDER BY salience DESC, created DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Memory
	for rows.Next() {
		var m Memory
		var kind string
		if err := rows.Scan(&m.ID, &m.Text, &kind, &m.Salience, &m.Confidence, &m.Project, &m.Source, &m.Agent, &m.Created, &m.LastUsed, &m.Uses, &m.SupersededBy, &m.Pin); err != nil {
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
		`SELECT id, text, kind, salience, confidence, project, source, agent, created, last_used, uses, superseded_by, pin
		   FROM memories
		  WHERE superseded = 0 AND quarantined = 0 AND (project = ? OR project = '')
		  ORDER BY salience DESC, created DESC`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Memory
	for rows.Next() {
		var m Memory
		var kind string
		if err := rows.Scan(&m.ID, &m.Text, &kind, &m.Salience, &m.Confidence, &m.Project, &m.Source, &m.Agent, &m.Created, &m.LastUsed, &m.Uses, &m.SupersededBy, &m.Pin); err != nil {
			return nil, err
		}
		m.Kind = Kind(kind)
		out = append(out, m)
	}
	return out, rows.Err()
}

// excludeNever drops PinNever memories from a slice that was not already
// filtered at the SQL level — the no-embedding fallback paths, which read
// through All/AllInProject rather than recallScoped. All itself stays
// unfiltered: it also backs the CLI listing, where a user managing what is
// excluded needs to be able to see it.
func excludeNever(mems []Memory) []Memory {
	out := mems[:0]
	for _, m := range mems {
		if m.Pin != PinNever {
			out = append(out, m)
		}
	}
	return out
}

// Pinned returns every active PinAlways memory in scope for a project — its
// own plus the global ones, the same scoping AllInProject uses. This is the
// list contextpack must include regardless of relevance score; ranking never
// gets a vote on whether a pinned memory appears; only the budget does, and
// only in the sense of how much of it fits — see spendMemories.
func Pinned(db *sql.DB, project string) ([]Memory, error) {
	q := `SELECT id, text, kind, salience, confidence, project, source, agent, created, last_used, uses, superseded_by, pin
	        FROM memories WHERE superseded = 0 AND quarantined = 0 AND pin = ?`
	args := []any{PinAlways}
	if strings.TrimSpace(project) != "" {
		q += " AND (project = ? OR project = '')"
		args = append(args, project)
	}
	q += " ORDER BY created ASC"
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Memory
	for rows.Next() {
		var m Memory
		var kind string
		if err := rows.Scan(&m.ID, &m.Text, &kind, &m.Salience, &m.Confidence, &m.Project, &m.Source, &m.Agent, &m.Created, &m.LastUsed, &m.Uses, &m.SupersededBy, &m.Pin); err != nil {
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
	var k string
	db.QueryRow("SELECT kind FROM memories WHERE id = ?", id).Scan(&k)
	if err := Reconcile(db, Kind(k)); err != nil {
		return err
	}
	kind, err := forgetRow(db, id)
	if err != nil {
		return err
	}
	// Forgetting has to reach the file too, or the next import restores it —
	// which makes a failure here worth reporting rather than swallowing.
	return flush(db, kind)
}

// forgetRow is Forget without the file write, for callers that are going to
// write the file themselves once at the end.
//
// Reconcile is the one that needs this: it deletes every memory the user
// removed from the file by hand, and flushing inside that loop would rewrite
// the file once per deletion — each time from a half-reconciled index, and each
// time over the very file still being read as the source of truth.
func forgetRow(db *sql.DB, id int64) (Kind, error) {
	// Snapshot the text into the log first, so the timeline records what was
	// forgotten even though the row is about to vanish.
	// The project comes out in the same snapshot: after the DELETE below there
	// is no row left to ask, and a forgotten memory that drops out of its own
	// project's timeline is the one omission a person would actually notice.
	var text, kind, proj string
	db.QueryRow("SELECT text, kind, project FROM memories WHERE id = ?", id).Scan(&text, &kind, &proj)
	if _, err := db.Exec("DELETE FROM memories WHERE id = ?", id); err != nil {
		return Kind(kind), err
	}
	logEventIn(db, id, EvForgotten, text, 0, proj)
	return Kind(kind), nil
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

// Pin marks a memory always-include: a context pack carries it regardless of
// relevance score. For the handful of things that are always worth having on
// hand — the standing instruction, the fact that governs everything else — and
// that a ranking model might otherwise rate as unremarkable on any given query.
func Pin(db *sql.DB, id int64) error { return setPin(db, id, PinAlways, "pinned") }

// Unpin returns a memory to normal ranking, whichever direction it was set —
// pinned or excluded.
func Unpin(db *sql.DB, id int64) error { return setPin(db, id, PinNone, "unpinned") }

// Exclude marks a memory never-include: it is dropped from recall entirely,
// but stays on record rather than being deleted. For the case Forget is too
// blunt for — a memory that is not wrong, just not something that should
// surface (something sensitive, something the user would rather manage by
// hand) — where the honest action is "stop showing me this", not "erase it".
func Exclude(db *sql.DB, id int64) error { return setPin(db, id, PinNever, "excluded") }

// setPin is Forget's pattern without the delete: snapshot for the log, change
// the row, log the event, flush to the vault so the state survives `rm -rf
// .brain`. Silently a no-op on an id that does not exist, matching Forget —
// see there for why that asymmetry (permissive here, not an error) is the
// existing behaviour rather than a gap introduced by this function.
func setPin(db *sql.DB, id int64, pin int, verb string) error {
	var text, kind string
	db.QueryRow("SELECT text, kind FROM memories WHERE id = ?", id).Scan(&text, &kind)
	// Same reason as Store: this ends in a whole-file rewrite, so whatever the
	// user edited by hand has to be adopted before it, not lost by it.
	if err := Reconcile(db, Kind(kind)); err != nil {
		return err
	}
	if _, err := db.Exec("UPDATE memories SET pin = ? WHERE id = ?", pin, id); err != nil {
		return err
	}
	logEvent(db, id, EvUpdated, verb+": "+text, 0)
	return flush(db, Kind(kind))
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

// boolToInt matches the rest of the package's raw-SQL style, which spells
// every other flag column (superseded, and every literal written into it) as
// a plain 0/1 rather than relying on the driver's bool coercion.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
