package contextpack

import (
	"fmt"
	"strings"
	"time"

	"github.com/Coder8124/logos/internal/activity"
	"github.com/Coder8124/logos/internal/session"
)

// RulingCost is what the latest checkpoint's ruled-out approaches took to
// learn, counted from records that already exist. A zero is "the record does
// not say", never "it cost nothing": a session the activity log never saw has
// no command count, and the render says nothing rather than print a zero that
// reads as a measurement.
type RulingCost struct {
	// FailedCommands is the failing shell commands the activity log holds for
	// the conversation that wrote the checkpoint, up to the checkpoint.
	FailedCommands int
	// EarlierSessions is the earlier conversations whose checkpoints recorded
	// one of the same rulings: each is a session that walked into the dead end
	// before this one did. Commit carries no ruling forward on its own, so a
	// repeat is an agent arriving there again, not a copy.
	EarlierSessions int
}

// loadRulingCost fills p.Cost. "Carrying 1 ruled-out approach" counts what was
// kept and says nothing about what keeping it saves, which is the one thing
// that makes the restore worth reading; the two numbers here are that, from
// the vault's own files, so deleting the index changes neither.
func (p *Pack) loadRulingCost(vault string) {
	c := p.Checkpoint
	if c == nil || vault == "" || p.historyScope == "" {
		return
	}
	keys := map[string]bool{}
	for _, f := range c.Failed {
		if !session.IsPlaceholder(f) {
			keys[normalizeKey(f)] = true
		}
	}
	if len(keys) == 0 {
		return
	}
	var cost RulingCost
	// DecisionWindow is the depth "Decided earlier" already reads, so this
	// looks no further back than the pack does for decisions.
	if all, err := session.History(vault, p.historyScope, session.DecisionWindow); err == nil {
		seen := map[string]bool{}
		for _, old := range all {
			// The same sitting checkpointed twice is one session, not a
			// predecessor; without a host id the checkpoint is its own.
			id := old.HostSession
			if id == "" {
				id = old.Slug
			}
			if old.Slug == c.Slug || (c.HostSession != "" && id == c.HostSession) || seen[id] {
				continue
			}
			for _, f := range old.Failed {
				if keys[normalizeKey(f)] {
					seen[id] = true
					cost.EarlierSessions++
					break
				}
			}
		}
	}
	// Recording off means the log was not kept, so a count from it would be a
	// count of what happened to survive. Bash only, as package claims reads it:
	// a Read of a missing file fails in nearly every session and rules nothing
	// out.
	if c.HostSession != "" && activity.Recording(vault) {
		events, err := activity.Read(vault, activity.Query{Kind: activity.KindTool, Tool: "Bash", Until: time.Unix(c.TS, 0)})
		if err == nil {
			short := activity.ShortID(c.HostSession)
			for _, e := range events {
				if e.Failed && e.Session == short && (e.Project == "" || c.Project == "" || e.Project == c.Project) {
					cost.FailedCommands++
				}
			}
		}
	}
	if cost != (RulingCost{}) {
		p.Cost = &cost
	}
}

// costLine renders p.Cost as one sentence the SessionStart hook lifts into its
// banner verbatim, so the banner and the pack cannot disagree. It sits directly
// under the ruled-out bullets, where vault text cannot reach: every line there
// is a bullet the renderer wrote.
func (p *Pack) costLine() string {
	if p.Cost == nil || p.Checkpoint == nil {
		return ""
	}
	n := 0
	for _, f := range p.Checkpoint.Failed {
		if !session.IsPlaceholder(f) {
			n++
		}
	}
	them := plural(n, "it", "them")
	var parts []string
	if k := p.Cost.FailedCommands; k > 0 {
		parts = append(parts, fmt.Sprintf("%d failed %s in the session that ruled %s out", k, plural(k, "command", "commands"), them))
	}
	if k := p.Cost.EarlierSessions; k > 0 {
		parts = append(parts, fmt.Sprintf("%d earlier %s that hit the same dead end", k, plural(k, "session", "sessions")))
	}
	if len(parts) == 0 {
		return ""
	}
	return fmt.Sprintf("Re-deriving %s would repeat %s.", them, strings.Join(parts, ", and "))
}
