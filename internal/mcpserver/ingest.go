package mcpserver

import (
	"fmt"
	"strings"

	"github.com/Coder8124/brain/internal/ingest"
	"github.com/Coder8124/brain/internal/text"
)

// Distillation done by the agent that asked for the ingest.
//
// The agent on the other end of this server is usually running a model far
// stronger than any local T1 tier will hold, is already in the user's session
// and is already paid for. It is the best distiller available and on most
// installs the only one. So these two tools hand it a harvest and take the
// judgement back.
//
// What they deliberately do not do:
//
//   - ingest_harvest never discovers a transcript. It serves only sessions a
//     `brain ingest` already read and queued, so the CLI stays the only thing
//     that can decide to read a new file off disk (Part D), and the consent
//     grant stays the gate.
//   - ingest_distil writes a *candidate*. A promotion is still a person
//     deciding, and nothing here shortens that path.
//   - The citation filter is the same one a 4B local model would face. A
//     stronger distiller is not a more trusted one.

// ingestHarvest serves one pending candidate's evidence, or lists what is
// waiting when no session was named.
func (s *Session) ingestHarvest(ref string, maxTurns int) (string, error) {
	if strings.TrimSpace(ref) == "" {
		pending, _ := ingest.Pending(s.vault)
		if len(pending) == 0 {
			return s.receipt("no ingested sessions are waiting to be distilled") +
				"\n\nNothing is queued. `brain ingest` reads transcripts; this tool only serves what it already queued.", nil
		}
		var b strings.Builder
		b.WriteString(s.receipt(fmt.Sprintf("%d ingested session(s) waiting to be distilled", len(pending))))
		b.WriteString("\n\n")
		for _, c := range pending {
			fmt.Fprintf(&b, "  %s  %s session %s  (%d turns, %d commands, %s)\n",
				orUnattributedProject(c.Project), c.Harness, shortSession(c.SessionID), c.TurnCount, len(c.Commands), c.Tier)
		}
		b.WriteString("\nCall ingest_harvest again with one session id to see its evidence.\n")
		return b.String(), nil
	}

	ev, err := ingest.EvidenceFor(s.vault, ref, maxTurns)
	if err != nil {
		return "", err
	}
	// Remember how much of the session was actually shown. The filter refuses a
	// citation to an abridged turn, which is only meaningful if the write half
	// knows what the read half rendered — otherwise a long session is served in
	// two hundred turns and validated against two thousand.
	if s.served == nil {
		s.served = map[string]int{}
	}
	s.served[ev.Candidate.SessionID] = maxTurns
	return s.receipt(fmt.Sprintf("served the harvest for %s session %s", ev.Candidate.Harness, shortSession(ev.Candidate.SessionID))) +
		"\n\n" + ev.Render(), nil
}

// ingestDistil takes the distillation back, filters it, and rewrites the
// candidate. Dropped claims are named: a distillation that lost half its
// entries must read as one that lost half its entries (invariant 4).
func (s *Session) ingestDistil(ref, model, next string, verified, failed, blockers []string) (string, error) {
	if strings.TrimSpace(ref) == "" {
		return "", fmt.Errorf("ingest_distil needs the session it is distilling — call ingest_harvest first")
	}
	c, drops, redactions, err := ingest.AcceptWithin(s.vault, ref, s.servedWindow(ref), ingest.Distillation{
		Model:    model,
		Verified: verified,
		Failed:   failed,
		Blockers: blockers,
		Next:     next,
	})
	if err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString(s.receipt(fmt.Sprintf("distilled %s session %s into a candidate — %d verified, %d didn't work",
		c.Harness, shortSession(c.SessionID), len(c.Verified), len(c.Failed))))
	b.WriteString("\n\n")
	if len(redactions) > 0 {
		fmt.Fprintf(&b, "%d secret-shaped token(s) redacted before writing:\n", len(redactions))
		for _, r := range redactions {
			fmt.Fprintf(&b, "  %s: %s\n", r.Field, r.Reason)
		}
		b.WriteString("\n")
	}
	if len(drops) > 0 {
		fmt.Fprintf(&b, "%d claim(s) dropped by the citation filter:\n", len(drops))
		for _, d := range drops {
			fmt.Fprintf(&b, "  %s: %s — %s\n", d.Field, clipClaim(d.Claim), d.Reason)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "It is still a candidate, not a checkpoint. A person promotes it:\n  brain ingest review --promote %s\n", shortSession(c.SessionID))
	return b.String(), nil
}

func clipClaim(s string) string {
	return text.Ellipsize(s, 80)
}

func shortSession(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func orUnattributedProject(p string) string {
	if strings.TrimSpace(p) == "" {
		return "(unattributed)"
	}
	return p
}

// servedWindow is the abridgement this session actually rendered for ref, or
// zero when it served nothing — a host that calls ingest_distil without
// ingest_harvest gets the default window rather than a free pass.
func (s *Session) servedWindow(ref string) int {
	for id, n := range s.served {
		if id == ref || strings.HasPrefix(id, ref) {
			return n
		}
	}
	return 0
}
