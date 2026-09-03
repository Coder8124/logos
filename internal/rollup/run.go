package rollup

import (
	"database/sql"
	"fmt"
	"os"
	"time"

	"github.com/Coder8124/brain/internal/capture"
	"github.com/Coder8124/brain/internal/router"
	"github.com/Coder8124/brain/internal/secretary"
)

// Run rolls a single day up into a daily note and a set of proposals.
//
// The order matters: entities are resolved against the existing vault *before*
// any note is proposed, so a near-duplicate becomes a merge proposal rather
// than a second note.
type Result struct {
	Sessions  int
	DailyPath string
	Proposals int
	Skipped   int
	Loops     int
}

func Day(db *sql.DB, vaultDir string, rt *router.Router, date time.Time, dryRun bool) (Result, error) {
	var res Result

	start := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, date.Location())
	events, err := capture.Range(db, start.Unix(), start.AddDate(0, 0, 1).Unix())
	if err != nil {
		return res, err
	}

	sessions := Sessionise(events)
	res.Sessions = len(sessions)
	if len(sessions) == 0 {
		return res, nil
	}

	x := NewExtractor(rt)
	dateStr := start.Format("2006-01-02")

	var classes []Classification
	var proposals []Proposal
	byTarget := map[string]int{}

	for _, s := range sessions {
		class, model, err := x.ClassifySession(s)
		if err != nil {
			fmt.Fprintf(os.Stderr, "· classify failed for %s: %v\n", hhmm(s.Start), err)
			continue
		}
		classes = append(classes, class)

		// Idle stretches carry no information worth a proposal.
		if class.Category == Idle {
			res.Skipped++
			continue
		}

		entities, _, err := x.ExtractEntities(s)
		if err != nil {
			fmt.Fprintf(os.Stderr, "· entity extraction failed for %s: %v\n", hhmm(s.Start), err)
			continue
		}

		for _, e := range entities {
			p, ok := proposeForEntity(db, e, class, s, model)
			if !ok {
				continue
			}
			// The same entity across several sessions is one proposal with the
			// union of the evidence, not one per session. Four identical
			// entries in the queue is four times the review cost for the same
			// decision.
			if prev, seen := byTarget[p.Target]; seen {
				merged := proposals[prev]
				merged.Evidence = append(merged.Evidence, p.Evidence...)
				if p.Conf > merged.Conf {
					merged.Conf = p.Conf
					merged.Payload = p.Payload
				}
				proposals[prev] = merged
				continue
			}
			byTarget[p.Target] = len(proposals)
			proposals = append(proposals, p)
		}
	}

	// The daily note is written directly rather than proposed: it is a log of
	// what was observed, not an inference about the world, and it lands in its
	// own file that nothing else links into.
	body, _, err := x.WriteDaily(dateStr, sessions, classes)
	if err != nil {
		return res, fmt.Errorf("writing daily note: %w", err)
	}

	dailySlug := "daily/" + dateStr
	if res.DailyPath, err = notePath(vaultDir, dailySlug); err != nil {
		return res, err
	}

	if !dryRun {
		if err := writeDaily(vaultDir, dailySlug, dateStr, body, sessions); err != nil {
			return res, err
		}
		// Pull open loops out of the day's log while we have it in hand. A
		// secretary that never notices what you said you would do is just an
		// archive with better prose.
		if err := secretary.Init(db); err == nil {
			if loops, err := secretary.Extract(rt, body, dailySlug); err == nil {
				for _, c := range loops {
					secretary.Add(db, &c)
				}
				res.Loops = len(loops)
			}
		}
		for i := range proposals {
			if err := Enqueue(db, &proposals[i]); err != nil {
				fmt.Fprintf(os.Stderr, "· rejected malformed proposal: %v\n", err)
				continue
			}
			res.Proposals++
		}
	} else {
		res.Proposals = len(proposals)
	}

	return res, nil
}

// proposeForEntity decides what, if anything, to propose about one extracted
// entity. Resolution happens here — before creation, never after.
func proposeForEntity(db *sql.DB, e Entity, class Classification, s Session, model string) (Proposal, bool) {
	base := Proposal{
		Conf:     confidenceFor(class, s),
		Evidence: s.EventIDs,
		Model:    model,
		Created:  s.Start,
	}

	if m, found := Resolve(db, e.Name, e.Type); found {
		base.Kind = Append
		base.Target = m.Slug
		base.Payload = Payload{Body: class.Summary}
		return base, true
	}

	// Not an existing note. Before creating one, check for a near-duplicate —
	// this is what stops "Sam" and "Sameer" becoming two people.
	if near := NearMiss(db, e.Name, e.Type); len(near) > 0 {
		base.Kind = Merge
		base.Target = e.Type + "s/" + normalise(e.Name)
		base.Payload = Payload{Into: near[0], Title: e.Name}
		// A merge is a question, not a claim; cap the confidence so it never
		// crosses an auto-accept threshold.
		base.Conf = min(base.Conf, 0.5)
		return base, true
	}

	base.Kind = NewNote
	base.Target = e.Type + "s/" + normalise(e.Name)
	base.Payload = Payload{Title: e.Name, Type: e.Type, Body: class.Summary}
	return base, true
}

// confidenceFor scores a proposal by how much observation stands behind it.
// A five-minute session is weak evidence; two hours is not.
func confidenceFor(class Classification, s Session) float64 {
	conf := 0.5
	switch class.Category {
	case Work:
		conf = 0.7
	case Research:
		conf = 0.6
	case Comms:
		conf = 0.55
	}
	switch {
	case s.DurS() > 3600:
		conf += 0.15
	case s.DurS() > 900:
		conf += 0.05
	case s.DurS() < 300:
		conf -= 0.15
	}
	return max(0.05, min(0.95, conf))
}

func writeDaily(vaultDir, slug, date, body string, sessions []Session) error {
	var b []byte
	b = append(b, fmt.Sprintf("---\ntype: daily\ndate: %s\nsessions: %d\n---\n\n", date, len(sessions))...)
	b = append(b, body...)
	b = append(b, "\n\n"+ObservationsHeading+"\n\n"...)
	for _, s := range sessions {
		b = append(b, fmt.Sprintf("- %s–%s (%dm)\n", hhmm(s.Start), hhmm(s.End), s.DurS()/60)...)
	}
	path, err := notePath(vaultDir, slug)
	if err != nil {
		return err
	}
	return writeAtomic(path, b)
}

func normalise(name string) string {
	out := make([]rune, 0, len(name))
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			out = append(out, r)
		case r >= 'A' && r <= 'Z':
			out = append(out, r+32)
		case r == ' ' || r == '_' || r == '-':
			out = append(out, '-')
		}
	}
	return string(out)
}

func hhmm(ts int64) string { return time.Unix(ts, 0).Local().Format("15:04") }
