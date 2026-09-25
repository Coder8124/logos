package ingest

import "github.com/Coder8124/logos/internal/secret"

// A transcript is someone else's agent talking to a shell: it routinely
// contains a pasted API key, a curl command with a bearer token, or an
// Authorization header copied into a debugging session. Harvest and distil
// both turn transcript text into candidate fields that get written to
// memories/*.md verbatim, so every one of them is masked first, with the
// transcript-strength rules — see package secret — and what was masked is
// said (invariant 3), never redacted silently.

// Redaction records one secret-shaped token that was masked before a
// candidate reached the vault.
type Redaction = secret.Redaction

// redactCandidateText masks secret-shaped tokens in every free-text field a
// harvest or distillation writes to the vault, mutating c in place, and
// reports what it found.
func redactCandidateText(c *Candidate) []Redaction {
	var found []Redaction
	redactSlice := func(field string, items []string) []string {
		out := make([]string, len(items))
		for i, it := range items {
			masked, r := redactText(field, it)
			out[i] = masked
			found = append(found, r...)
		}
		return out
	}
	c.Commands = redactSlice("Commands run", c.Commands)
	c.Verified = redactSlice("Verified", c.Verified)
	c.Failed = redactSlice("Didn't work", c.Failed)
	c.Blockers = redactSlice("Blockers", c.Blockers)
	next, r := redactText("Next", c.Next)
	c.Next = next
	found = append(found, r...)
	return found
}

// Redact masks every secret-shaped substring of s, for callers outside ingest
// that store text a host handed them without anyone choosing to write it.
func Redact(s string) string {
	out, _ := redactText("", s)
	return out
}

func redactText(field, s string) (string, []Redaction) {
	return secret.MaskAll(field, s)
}
