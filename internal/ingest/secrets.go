package ingest

import (
	"math"
	"regexp"
	"strings"
)

// A transcript is someone else's agent talking to a shell: it routinely
// contains a pasted API key, a curl command with a bearer token, or an
// Authorization header copied into a debugging session. Harvest and distil
// both turn transcript text into candidate fields that get written to
// memories/*.md verbatim, and nothing before this file stopped a live
// credential from riding along into the vault in plaintext. This is the
// minimum bar: detect known credential shapes
// and high-entropy tokens, mask them, and say what was masked (invariant 3) —
// never redact silently, and never let a masked value round-trip back to the
// looking-legitimate text it replaced.

// Redaction records one secret-shaped token that was masked before a
// candidate reached the vault, so the caller can announce it instead of
// leaving the fix invisible.
type Redaction struct {
	Field  string // candidate field the token was found in ("Commands", "Verified", ...)
	Reason string // why it matched
}

const redactedMarker = "[REDACTED]"

// authHeaderLine matches a whole "Authorization: ..." line — the header value
// itself is the credential, so the fix is to drop the line's payload rather
// than try to salvage the rest of it.
var authHeaderLine = regexp.MustCompile(`(?i)^(\s*)Authorization\s*:\s*\S.*$`)

// secretPrefixes are provider-issued token shapes specific enough that
// matching on the prefix alone is safe: nothing else legitimately starts this
// way in command output or a distilled claim.
var secretPrefixes = []string{
	"sk-", "sk_live_", "sk_test_", "rk_live_", // OpenAI / Stripe secret keys
	"ghp_", "gho_", "ghu_", "ghs_", "ghr_", "github_pat_", // GitHub tokens
	"AKIA", "ASIA", // AWS access key IDs
	"xox", // Slack tokens (xoxb-, xoxp-, xoxa-, ...)
}

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

// redactText masks every secret-shaped substring of s and reports what it
// found. field is carried through only for the report, not for matching.
func redactText(field, s string) (string, []Redaction) {
	if s == "" {
		return s, nil
	}
	var found []Redaction
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if m := authHeaderLine.FindStringSubmatch(line); m != nil {
			lines[i] = m[1] + "Authorization: " + redactedMarker
			found = append(found, Redaction{Field: field, Reason: "Authorization header"})
			continue
		}
		lines[i], found = redactTokens(field, line, found)
	}
	return strings.Join(lines, "\n"), found
}

// redactTokens walks whitespace-separated tokens in one line and masks any
// that look like a credential, preserving surrounding punctuation so the
// masked line still reads.
func redactTokens(field, line string, found []Redaction) (string, []Redaction) {
	words := strings.Fields(line)
	changed := false
	for i, w := range words {
		trimmed := strings.Trim(w, ",;:'\"()[]{}`")
		reason := secretReason(trimmed)
		if reason == "" {
			continue
		}
		words[i] = strings.Replace(w, trimmed, redactedMarker, 1)
		found = append(found, Redaction{Field: field, Reason: reason})
		changed = true
	}
	if !changed {
		return line, found
	}
	return strings.Join(words, " "), found
}

// secretReason reports why tok looks like a credential, or "" if it does not.
func secretReason(tok string) string {
	if tok == "" {
		return ""
	}
	for _, p := range secretPrefixes {
		if strings.HasPrefix(tok, p) && len(tok) >= len(p)+6 {
			return "known credential prefix (" + p + ")"
		}
	}
	if looksHighEntropy(tok) {
		return "high-entropy token"
	}
	return ""
}

// looksHighEntropy is deliberately conservative. A file path, a git commit
// hash, a UUID and a hyphenated slug are all long and made of hex-ish
// characters, and flagging every one of those would make the review queue
// unreadable — so this requires real character-class diversity (upper case
// mixed with lower case or digits, the shape almost every real token API key
// actually has) on top of the entropy bar, which a lowercase hex hash or a
// v4 UUID never clears.
func looksHighEntropy(tok string) bool {
	if len(tok) < 20 || len(tok) > 4096 {
		return false
	}
	var hasUpper, hasLower, hasDigit bool
	for _, r := range tok {
		switch {
		case r >= 'A' && r <= 'Z':
			hasUpper = true
		case r >= 'a' && r <= 'z':
			hasLower = true
		case r >= '0' && r <= '9':
			hasDigit = true
		case r == '-' || r == '_' || r == '.' || r == '/' || r == '+' || r == '=':
			// punctuation allowed inside a base64/JWT/path-shaped token; does
			// not by itself disqualify or qualify the token
		default:
			return false // anything else (spaces already split tokens) is prose
		}
	}
	if !hasUpper || !(hasLower || hasDigit) {
		return false
	}
	return shannonEntropy(tok) >= 3.5
}

func shannonEntropy(s string) float64 {
	freq := map[rune]int{}
	for _, r := range s {
		freq[r]++
	}
	n := float64(len(s))
	var e float64
	for _, c := range freq {
		p := float64(c) / n
		e -= p * math.Log2(p)
	}
	return e
}
