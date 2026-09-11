// Package ingest turns another coding agent's transcript into a Logos
// checkpoint *candidate* — never a checkpoint, and never by replaying the
// transcript. It is the back half of `brain ingest`; internal/transcript is the
// front half that reads the raw session.
//
// The thesis: a resumed transcript hands the next agent
// every abandoned approach as a live option, which is the stale-answer failure
// the continuity benchmark measures. So what lands in the vault is a distillate
// — files touched, commands run, and (with a model) verified/failed/next — plus
// a pointer back to the source. The transcript stays where it is: it routinely
// holds pasted secrets, customer data and dead-end reasoning the user never
// meant to persist.
//
// Vault-first, index-second (invariant 2). The queue and the "which sessions
// have been read" cursor are both derived from the candidate markdown files —
// nothing durable lives only in SQLite (invariant 1, which the review queue has
// already broken once).
package ingest

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Dir is where candidates live inside the vault. A visible folder: these are
// notes a user is meant to open and read before promoting.
const Dir = "ingest"

// Tiers. A harvest is mechanical facts only; a distilled candidate additionally
// carries verified/failed/next filled by a model, and says which one.
const (
	TierHarvest   = "harvest"
	TierDistilled = "distilled"
)

// Statuses. A candidate is pending until a human promotes or rejects it;
// promotion is the only path to a real checkpoint.
const (
	StatusPending  = "pending"
	StatusPromoted = "promoted"
	StatusRejected = "rejected"
)

// Candidate is one distilled session awaiting review.
type Candidate struct {
	Harness   string
	SessionID string
	Source    string // absolute path to the transcript, for provenance
	Hash      string // content hash of the source: the idempotence key
	Project   string // "" lands as unattributed for review to resolve
	Tier      string
	Model     string // the distiller, when Tier == distilled
	Status    string
	Started   int64
	Ended     int64
	Harvested int64

	Files    []string
	Commands []string // "<invocation> — ok" / " — failed"
	Verified []string
	Failed   []string
	Blockers []string
	Next     string

	TurnCount int // turn count of the source, for a sense of session size
	Skipped   int // malformed lines the reader stepped over (invariant 3)

	// FrontmatterUnreadable is set when the note's YAML frontmatter would not
	// parse. The candidate is kept anyway (its session id is recovered from the
	// filename) so a corrupt note never silently vanishes from the queue the way
	// a dropped candidate used to — but review must flag it rather than trust
	// stale-looking zero fields.
	FrontmatterUnreadable bool
}

const projectUnattributed = "unattributed"

// scope is the on-disk project folder. An unattributed candidate still needs a
// home; review reattributes it.
func (c Candidate) scope() string {
	if strings.TrimSpace(c.Project) == "" {
		return projectUnattributed
	}
	return safeSegment(c.Project)
}

// Filename is <harness>-<session-id>.md. The session id is the harness-native
// one, so re-running ingest lands on the same path and skips — the file's
// existence is the cursor.
func (c Candidate) Filename() string {
	return safeSegment(c.Harness) + "-" + safeSegment(c.SessionID) + ".md"
}

// sessionIDFromStem recovers a candidate's session id from its filename when the
// frontmatter is unreadable. Filename() builds the stem as
// <harness>-<session-id>.md, so stripping the extension and the known harness
// prefix inverts it; with no harness known, the whole stem is the best guess.
func sessionIDFromStem(filename, harness string) string {
	stem := strings.TrimSuffix(filepath.Base(filename), ".md")
	if harness != "" {
		stem = strings.TrimPrefix(stem, safeSegment(harness)+"-")
	}
	return stem
}

// RelPath is the candidate's slash path under the vault root.
func (c Candidate) RelPath() string {
	return filepath.ToSlash(filepath.Join(Dir, c.scope(), c.Filename()))
}

type candidateFM struct {
	Type      string `yaml:"type"`
	Harness   string `yaml:"harness"`
	Session   string `yaml:"session"`
	Source    string `yaml:"source"`
	Hash      string `yaml:"hash"`
	Project   string `yaml:"project"`
	Tier      string `yaml:"tier"`
	Model     string `yaml:"model"`
	Status    string `yaml:"status"`
	Started   string `yaml:"started"`
	Ended     string `yaml:"ended"`
	Harvested string `yaml:"harvested"`
	Turns     int    `yaml:"turns"`
	Skipped   int    `yaml:"skipped"`
}

// Markdown renders the candidate as a vault note. It is the inverse of Parse and
// the round trip is tested: that property is what lets the queue read the vault
// directly and survive a rebuilt index.
func (c Candidate) Markdown() string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("type: ingest_candidate\n")
	fmt.Fprintf(&b, "harness: %s\n", ys(c.Harness))
	fmt.Fprintf(&b, "session: %s\n", ys(c.SessionID))
	fmt.Fprintf(&b, "source: %s\n", ys(c.Source))
	fmt.Fprintf(&b, "hash: %s\n", ys(c.Hash))
	fmt.Fprintf(&b, "project: %s\n", ys(c.Project))
	fmt.Fprintf(&b, "tier: %s\n", ys(orDefault(c.Tier, TierHarvest)))
	if c.Model != "" {
		fmt.Fprintf(&b, "model: %s\n", ys(c.Model))
	}
	fmt.Fprintf(&b, "status: %s\n", ys(orDefault(c.Status, StatusPending)))
	if c.Started > 0 {
		fmt.Fprintf(&b, "started: %s\n", time.Unix(c.Started, 0).UTC().Format(time.RFC3339))
	}
	if c.Ended > 0 {
		fmt.Fprintf(&b, "ended: %s\n", time.Unix(c.Ended, 0).UTC().Format(time.RFC3339))
	}
	harvested := c.Harvested
	if harvested == 0 {
		harvested = time.Now().Unix()
	}
	fmt.Fprintf(&b, "harvested: %s\n", time.Unix(harvested, 0).UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "turns: %d\n", c.TurnCount)
	if c.Skipped > 0 {
		fmt.Fprintf(&b, "skipped: %d\n", c.Skipped)
	}
	// The distillate is data, retrieved from a transcript written by another
	// agent. It is rendered for a human to read, never obeyed (invariant 6).
	b.WriteString("relations:\n")
	fmt.Fprintf(&b, "  - { pred: ingested_from, obj: \"%s session %s\", conf: 1.0, src: stated }\n", c.Harness, c.SessionID)
	b.WriteString("---\n")

	summary := fmt.Sprintf("%s session %s, %d turns", c.Harness, c.SessionID, c.TurnCount)
	if c.Skipped > 0 {
		summary += fmt.Sprintf(" (%d unparsed line(s) skipped)", c.Skipped)
	}
	if c.Tier == TierHarvest || c.Tier == "" {
		summary += ". Harvest only — verified and failed are left empty because deriving them needs a model that was not available."
	}
	sec(&b, "State", summary)
	bul(&b, "Verified", c.Verified)
	bul(&b, "Didn't work", c.Failed)
	bul(&b, "Blockers", c.Blockers)
	bul(&b, "Commands run", c.Commands)
	bul(&b, "Files", c.Files)
	sec(&b, "Next", c.Next)
	return b.String()
}

// Parse reads a candidate back from its note. filename is the note's base name
// (<harness>-<session-id>.md); it is the fallback source of the session id when
// the frontmatter will not unmarshal, so a corrupt note is still a findable
// candidate rather than a silent drop.
func Parse(raw, filename string) Candidate {
	fmStr, body := splitFM(raw)
	var fm candidateFM
	fmUnreadable := false
	if fmStr != "" {
		// Contrast internal/vault/note.go, which also swallows a YAML error but
		// keeps the note; the queue previously dropped the candidate entirely
		// because SessionID came back empty. Keep it, and recover the id from
		// the filename stem the same way claudecode.go does for a transcript.
		if err := yaml.Unmarshal([]byte(fmStr), &fm); err != nil {
			fmUnreadable = true
		}
	}
	c := Candidate{
		Harness:   fm.Harness,
		SessionID: fm.Session,
		Source:    fm.Source,
		Hash:      fm.Hash,
		Project:   fm.Project,
		Tier:      orDefault(fm.Tier, TierHarvest),
		Model:     fm.Model,
		Status:    orDefault(fm.Status, StatusPending),
		TurnCount: fm.Turns,
		Skipped:   fm.Skipped,
	}
	if c.SessionID == "" {
		c.SessionID = sessionIDFromStem(filename, c.Harness)
	}
	c.FrontmatterUnreadable = fmUnreadable
	c.Started = parseTS(fm.Started)
	c.Ended = parseTS(fm.Ended)
	c.Harvested = parseTS(fm.Harvested)
	for _, s := range sections(body) {
		switch s.Heading {
		case "Verified":
			c.Verified = append(c.Verified, parseBullets(s.Text)...)
		case "Didn't work":
			c.Failed = append(c.Failed, parseBullets(s.Text)...)
		case "Blockers":
			c.Blockers = append(c.Blockers, parseBullets(s.Text)...)
		case "Commands run":
			c.Commands = append(c.Commands, parseBullets(s.Text)...)
		case "Files":
			c.Files = append(c.Files, parseBullets(s.Text)...)
		case "Next":
			c.Next = s.Text
		}
	}
	return c
}

func parseTS(s string) int64 {
	if s == "" {
		return 0
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Unix()
	}
	return 0
}
