// Package insight surfaces observations the vault already contains but
// nobody has stated out loud: a blocker that has survived several checkpoints
// without being resolved, a memory nobody has drawn on in months. Nothing
// here is invented — every generator reads checkpoints, memories or notes
// already in the vault and reports what pattern it found in them.
//
// This ships one tier: mechanical, vault-only generators that need no model
// and no network. Filter enforces the same rule internal/ingest's distiller
// pipeline applies to a claim: an insight that cannot name the checkpoint or
// memory it came from is dropped before it is shown, not shown with a
// caveat (see internal/ingest/distil.go's doc comment on Filter). A stronger
// generator is not a more trusted one, and an insight is a claim like any
// other — the citation rule is the same rule for the same reason.
package insight

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Coder8124/brain/internal/memory"
	"github.com/Coder8124/brain/internal/session"
	"github.com/Coder8124/brain/internal/textmatch"
)

// Insight is one observation, always traceable to what produced it.
type Insight struct {
	Kind    string   // "recurring-blocker", "dormant-memory"
	Text    string   // what to tell the user, in plain language
	Sources []string // note path, "memory#<id>", or a checkpoint's vault slug
}

// Drop is an insight the filter refused, and why. Dropped insights are
// returned, never discarded quietly (invariant 4) — a generator that found
// ten candidates and shipped six must not read as one that only ever found
// six.
type Drop struct {
	Claim  string
	Reason string
}

// Filter applies the citation rule: no source, no insight. It is the same
// shape as internal/ingest.Filter for the same reason — a claim nobody can
// trace back to something in the vault is a fabrication with good manners,
// whether it came from a distiller reading a transcript or a generator
// reading a checkpoint.
func Filter(insights []Insight) ([]Insight, []Drop) {
	var kept []Insight
	var drops []Drop
	for _, in := range insights {
		text := strings.TrimSpace(in.Text)
		if text == "" {
			drops = append(drops, Drop{Claim: in.Text, Reason: "empty insight"})
			continue
		}
		if len(in.Sources) == 0 {
			drops = append(drops, Drop{Claim: text, Reason: "no source cited"})
			continue
		}
		kept = append(kept, in)
	}
	return kept, drops
}

// Degraded explains, in one sentence, why Generate never does more than the
// mechanical tier: there is currently no model-augmented generator at all.
// It exists so a caller can announce the degradation (invariant 3) instead of
// a short list quietly reading as "there was nothing to find".
const Degraded = "mechanical tier only — no model-augmented insight generator exists yet, so every insight below came from pattern-matching the vault, not a model reading it"

// recurringBlockerWindow bounds how far back a generator looks for a blocker
// repeating. Insight is about a standing problem, not archaeology.
const recurringBlockerWindow = 8

// dormantAfter is how long a memory can go unused before it is worth a
// second look. Long enough that an ordinary lull between sessions on a
// project doesn't trip it, short enough that it still catches something a
// user genuinely forgot they knew.
const dormantAfter = 60 * 24 * time.Hour

// blockerAkin treats two blockers as the same standing problem when they
// share more than half their meaningful words — restated in different words
// across two checkpoints is still the same blocker, and exact-string
// matching would miss almost every real recurrence.
func blockerAkin(a, b string) bool {
	sa, sb := textmatch.Subject(a), textmatch.Subject(b)
	if len(sa) == 0 || len(sb) == 0 {
		return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
	}
	return textmatch.Overlap(sa, sb) >= 0.5
}

// Scan is what Generate looked at. It exists because "0 insights found" on
// its own is indistinguishable from a command that declined to run, and an
// empty state a user cannot tell apart from a broken one is the empty state
// they close the tool over. Reporting the size of the haystack turns it into
// "looked at this much, found nothing" (invariant 3).
type Scan struct {
	Projects    int
	Checkpoints int
	Memories    int
}

// Generate produces every insight the mechanical generators can find. project
// scopes to one project's checkpoints and memories; "" scans every project
// session.Projects lists and every memory regardless of project.
//
// Callers must have already run memory.Init and session.Init against db — the
// same expectation every other package taking a *sql.DB in this codebase
// makes, so this package does not duplicate schema setup that belongs to the
// packages that own those tables.
func Generate(db *sql.DB, vaultDir, project string) ([]Insight, []Drop, Scan, error) {
	var all []Insight
	var scan Scan

	rb, checkpoints, projects, err := recurringBlockers(vaultDir, project)
	if err != nil {
		return nil, nil, scan, fmt.Errorf("scanning checkpoints for recurring blockers: %w", err)
	}
	all = append(all, rb...)
	scan.Checkpoints, scan.Projects = checkpoints, projects

	dm, memories, err := dormantMemories(db, project, time.Now())
	if err != nil {
		return nil, nil, scan, fmt.Errorf("scanning memories for dormant ones: %w", err)
	}
	all = append(all, dm...)
	scan.Memories = memories

	kept, drops := Filter(all)
	sort.SliceStable(kept, func(i, j int) bool {
		if kept[i].Kind != kept[j].Kind {
			return kept[i].Kind < kept[j].Kind
		}
		return kept[i].Text < kept[j].Text
	})
	return kept, drops, scan, nil
}

// recurringBlockers finds, per project, a blocker that appears — in the same
// or different words — in two or more of the last recurringBlockerWindow
// checkpoints. A blocker mentioned once is just a blocker; one that survives
// across checkpoints is a standing problem nobody has come back to.
//
// It clusters across the whole window rather than seeding from the most recent
// checkpoint's blockers, because seeding from the latest made the generator
// blind in the case it exists for: a real vault whose newest checkpoint said
// "none currently known" reported nothing at all, hiding every standing
// problem in the eight checkpoints behind it.
func recurringBlockers(vaultDir, project string) (found []Insight, checkpoints, scanned int, err error) {
	projects := []string{project}
	if project == "" {
		all, err := session.Projects(vaultDir)
		if err != nil {
			return nil, 0, 0, err
		}
		projects = all
	}

	// One standing problem, however many checkpoints and wordings it has.
	type cluster struct {
		text    string // the newest wording, since history is newest-first
		sources []string
	}

	var out []Insight
	for _, p := range projects {
		history, err := session.History(vaultDir, p, recurringBlockerWindow)
		if err != nil {
			return nil, 0, 0, err
		}
		// Counted before the two-checkpoint guard: a project with one
		// checkpoint was still looked at, and saying otherwise understates
		// the haystack the caller is about to report.
		checkpoints += len(history)
		if len(history) < 2 {
			continue
		}

		var clusters []*cluster
		for _, cp := range history {
			for _, blocker := range cp.Blockers {
				var hit *cluster
				for _, c := range clusters {
					if blockerAkin(blocker, c.text) {
						hit = c
						break
					}
				}
				if hit == nil {
					clusters = append(clusters, &cluster{text: blocker, sources: []string{cp.Slug}})
					continue
				}
				// One checkpoint restating the same blocker twice is one
				// appearance. Counting it twice would report a standing
				// problem that has only ever been raised once.
				if hit.sources[len(hit.sources)-1] != cp.Slug {
					hit.sources = append(hit.sources, cp.Slug)
				}
			}
		}

		for _, c := range clusters {
			if len(c.sources) < 2 {
				continue
			}
			out = append(out, Insight{
				Kind:    "recurring-blocker",
				Text:    fmt.Sprintf("%s — still blocking %s after %d checkpoints", c.text, p, len(c.sources)),
				Sources: c.sources,
			})
		}
	}
	return out, checkpoints, len(projects), nil
}

// dormantMemories finds a memory nobody has drawn on in dormantAfter, so it
// can be re-confirmed, re-pinned, or forgotten rather than silently going
// stale. A memory already excluded from recall (PinNever) is not this
// generator's business — its owner already decided to stop hearing about it.
func dormantMemories(db *sql.DB, project string, now time.Time) ([]Insight, int, error) {
	var mems []memory.Memory
	var err error
	if project == "" {
		mems, err = memory.All(db)
	} else {
		mems, err = memory.AllInProject(db, project)
	}
	if err != nil {
		return nil, 0, err
	}

	cutoff := now.Add(-dormantAfter).Unix()
	var out []Insight
	for _, m := range mems {
		if m.Pin == memory.PinNever {
			continue
		}
		last := m.LastUsed
		if last == 0 {
			last = m.Created
		}
		if last == 0 || last > cutoff {
			continue
		}
		days := int(now.Unix()-last) / 86400
		out = append(out, Insight{
			Kind:    "dormant-memory",
			Text:    fmt.Sprintf("%q hasn't been drawn on in %d days — worth re-confirming or forgetting", m.Text, days),
			Sources: []string{fmt.Sprintf("memory#%d", m.ID)},
		})
	}
	return out, len(mems), nil
}
