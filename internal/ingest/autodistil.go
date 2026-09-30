package ingest

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Coder8124/logos/internal/session"
	"github.com/Coder8124/logos/internal/transcript"
)

// Distilling an auto record.
//
// An auto record lists what a session ran and nothing it learned, because the
// session ended before its agent said. The transcript it was built from still
// holds the rest, and the agent resuming the project is the one placed to read
// it: already running, already paid for, and deciding for itself whether that
// session bears on its task. So resume offers the record's evidence through
// ingest_harvest and takes the reading back through ingest_distil, under the
// same citation filter a queued candidate faces.
//
// What comes back is written into the record as inferred, beside what ran, and
// never into Failed or Verified: those are an agent's own testimony, and a
// failed entry is a ruling the next agent will not re-try.
//
// No consent grant is asked for here, unlike a queued candidate. The record
// exists because Logos already read this transcript to write it, and the only
// file opened is the one it names — nothing is discovered.

// autoSource reads which transcript an auto record came from off its state:
// "Built from cursor's own transcript (id)" or, from the Claude Code hook,
// "Built from the activity log (id)", where the agent is the harness.
var autoSource = regexp.MustCompile(`^Built from (?:(\S+)'s own transcript|the activity log) \(([^)]+)\)`)

// AutoTranscript is the harness and transcript id an auto record names, and
// false when it names none — a record written before the hook named its session.
func AutoTranscript(c session.Checkpoint) (harness, id string, ok bool) {
	if !c.Auto {
		return "", "", false
	}
	m := autoSource.FindStringSubmatch(strings.TrimSpace(c.State))
	if m == nil {
		return "", "", false
	}
	harness = m[1]
	if harness == "" {
		harness = c.Agent
	}
	return harness, m[2], harness != ""
}

// findTranscript opens the transcript harness wrote under id. A seam: tests
// need no real host on this machine.
var findTranscript = func(harness, id string) (*transcript.Session, error) {
	paths, err := transcript.Sessions(harness)
	if err != nil {
		return nil, err
	}
	for _, p := range paths {
		if transcriptID(p) == id {
			return transcript.ReadFile(harness, p)
		}
	}
	return nil, fmt.Errorf("%s no longer has transcript %s", harness, id)
}

// TranscriptTurns is how many turns the transcript harness wrote under id holds
// now, for a record built without reading it (the Claude Code hook's) to say
// how much of it the record covers.
func TranscriptTurns(harness, id string) (int, error) {
	s, err := findTranscript(harness, id)
	if err != nil {
		return 0, err
	}
	return len(s.Turns), nil
}

// IsRecordRef reports whether ref names a checkpoint file rather than a queued
// candidate: the slug resume prints, "sessions/<project>/<id>".
func IsRecordRef(ref string) bool {
	return strings.HasPrefix(RecordSlug(ref), session.CheckpointDir+"/")
}

// RecordSlug is ref as the record's slug, however an agent spelled it: with
// the file's .md, stray space, or backslashes. Harvest and distil must key the
// served window by the same string, or the filter checks a distillation
// against a window the distiller never saw.
func RecordSlug(ref string) string {
	return strings.TrimSuffix(filepath.ToSlash(strings.TrimSpace(ref)), ".md")
}

// AutoEvidenceFor serves the part of an auto record's transcript the record
// covers, numbered from one: the turns up to where the transcript stood when
// the record was written, after the last record of the same transcript and
// after its agent's last handoff — as the record itself was built from them.
func AutoEvidenceFor(vaultDir, slug string, maxTurns int) (Evidence, error) {
	slug = RecordSlug(slug)
	if strings.Contains(slug, "..") {
		return Evidence{}, fmt.Errorf("%q is not a checkpoint in this vault", slug)
	}
	raw, err := os.ReadFile(filepath.Join(vaultDir, filepath.FromSlash(slug)+".md"))
	if err != nil {
		return Evidence{}, fmt.Errorf("no checkpoint %s in this vault", slug)
	}
	c := session.ParseCheckpoint(string(raw))
	harness, id, ok := AutoTranscript(c)
	if !c.Auto {
		return Evidence{}, fmt.Errorf("%s was written by an agent, not built from a transcript — there is nothing to infer into it", slug)
	}
	if !ok {
		return Evidence{}, fmt.Errorf("%s does not name the transcript it was built from, so there is nothing to serve", slug)
	}
	if c.Turns <= 0 {
		return Evidence{}, fmt.Errorf("%s was written before auto records noted how much of their transcript they cover, so which turns are its own is unknown and nothing is served", slug)
	}
	s, err := findTranscript(harness, id)
	if err != nil {
		return Evidence{}, fmt.Errorf("reading the transcript %s was built from: %w", slug, err)
	}
	if len(s.Turns) < c.Turns {
		return Evidence{}, fmt.Errorf("the transcript %s was built from now holds %d turns, fewer than the %d it covered — it was rewritten, and nothing is served", slug, len(s.Turns), c.Turns)
	}
	start, err := earlierCoverage(vaultDir, c, slug)
	if err != nil {
		return Evidence{}, err
	}
	rest := *s
	rest.Turns = s.Turns[start:c.Turns]
	s = afterHandoff(&rest)
	cand := Harvest(s)
	cand.Project = c.Project
	ev := serve(Evidence{Candidate: cand, Turns: s.Turns, Record: slug}, maxTurns)
	ev.Covers = c.Turns
	return ev, nil
}

// earlierCoverage is where the last record of the same transcript before c
// stopped. A session resumed after its record was written gets a second
// record for what followed, and the first one's work is not the second's.
func earlierCoverage(vaultDir string, c session.Checkpoint, slug string) (int, error) {
	history, err := session.History(vaultDir, c.Project, 0)
	if err != nil {
		return 0, fmt.Errorf("reading the records beside %s: %w", slug, err)
	}
	start := 0
	for _, h := range history {
		if h.Slug != slug && h.Auto && h.State == c.State && h.Turns < c.Turns && h.Turns > start {
			start = h.Turns
		}
	}
	return start, nil
}

// AcceptAuto filters a distillation of an auto record's transcript against the
// window the distiller was served and writes what survives into the record as
// inferred. Claims are masked before the write, as every other path masks
// them: a paraphrase of a transcript carries a pasted key as easily as the
// transcript did.
func AcceptAuto(vaultDir, slug string, maxTurns int, d Distillation) (session.Checkpoint, []Drop, []Redaction, error) {
	ev, err := AutoEvidenceFor(vaultDir, slug, maxTurns)
	if err != nil {
		return session.Checkpoint{}, nil, nil, err
	}
	kept, drops := Filter(ev, d)
	var found []Redaction
	mask := func(field string, items []string) []string {
		out := make([]string, len(items))
		for i, it := range items {
			var r []Redaction
			out[i], r = redactText(field, it)
			found = append(found, r...)
		}
		return out
	}
	c, err := session.InferAuto(vaultDir, ev.Record, session.Inference{
		Turns:    ev.Covers,
		By:       orDefault(strings.TrimSpace(d.Model), "calling agent"),
		Verified: mask("Verified", kept.Verified),
		Failed:   mask("Didn't work", kept.Failed),
		Decided:  mask("Decided", kept.Decided),
	})
	return c, drops, found, err
}
