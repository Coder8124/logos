// Package secret masks credentials in text before it is written to the vault.
//
// It started in internal/ingest, where a transcript is someone else's agent
// talking to a shell and routinely carries a pasted API key. But an agent
// writing a checkpoint, a working note or a memory pastes the same keys —
// "verified: the upload works with GITHUB_TOKEN=ghp_…" — and those paths went
// to the vault verbatim (#209). It is a leaf package so session and memory can
// use it without importing ingest, which imports them.
//
// Two strengths, because the two kinds of text differ. A transcript is mostly
// machine output nobody chose to keep, so ingest also masks anything that
// merely looks random (MaskAll). An agent's own write is prose it chose, full
// of long mixed-case words — test names, module paths, identifiers with digits
// in them — that the entropy check can take for a key (#210 spares the plainest
// of them, not all) and that are the whole content of a verified line; there
// only a known credential shape is masked (Mask).
package secret

import (
	"fmt"
	"math"
	"regexp"
	"strings"
)

// Redaction records one secret-shaped token that was masked, so the caller
// can announce it instead of leaving the fix invisible.
type Redaction struct {
	Field  string // field the token was found in ("Commands", "Verified", ...)
	Reason string // why it matched
}

// Marker replaces what was masked.
const Marker = "[REDACTED]"

// Mask masks every substring of s that has a known credential shape. For text
// an agent chose to write: see the package comment for why it leaves
// high-entropy words alone. field is carried through only for the report.
func Mask(field, s string) (string, []Redaction) {
	return mask(field, s, false)
}

// MaskAll is Mask plus any token that looks random enough to be a credential
// with no recognisable shape. For transcript text.
func MaskAll(field, s string) (string, []Redaction) {
	return mask(field, s, true)
}

// Summary is the sentence a receipt carries when something was masked, or ""
// when nothing was. It names where and why, never the value: an agent that
// pasted a key and is told only "saved" goes on believing the key is in the
// vault, or never learns it pasted one at all.
func Summary(found []Redaction) string {
	if len(found) == 0 {
		return ""
	}
	var where []string
	seen := map[Redaction]bool{}
	for _, r := range found {
		if !seen[r] {
			seen[r] = true
			where = append(where, r.Field+": "+r.Reason)
		}
	}
	return fmt.Sprintf("%d secret-shaped token(s) redacted before writing (%s)", len(found), strings.Join(where, "; "))
}

// authHeaderLine matches a whole "Authorization: ..." line in a transcript — the header value
// itself is the credential, so the fix is to drop the line's payload rather
// than try to salvage the rest of it.
var authHeaderLine = regexp.MustCompile(`(?i)^(\s*)Authorization\s*:\s*\S.*$`)

// authHeaderInline is the same header inside a command — `curl -H
// "Authorization: Bearer <token>"` — where dropping the rest of the line would
// eat the URL. It masks up to the closing quote, or the end of the token when
// there is none. The scheme is masked too: it is short, and a Basic value is
// only base64 of user:password.
var authHeaderInline = regexp.MustCompile(`(?i)\bAuthorization\s*:\s*(?:(?:Bearer|Basic|Token)\s+)?[^\s'"]+`)

// authSchemed is the only header shape Mask trusts. In an agent's own prose
// "Authorization:" is as often a heading or a word ("Authorization: every
// route checks the session cookie") as a header, and masking what followed it
// threw the sentence away and reported a secret that was never there. A scheme
// followed by a value is a credential's structure; the value is still checked
// in plainWord, because "Bearer tokens expire hourly" has the structure too.
var (
	authSchemed = regexp.MustCompile(`(?i)\bAuthorization\s*:\s*(?:Bearer|Basic|Token|Digest|Bot)\s+([^\s'"]+)`)
	plainWord   = regexp.MustCompile(`^[a-z]+[.,;:!?]?$`)
)

// urlPassword is the password in a URL's user:password@host. It is almost
// never high-entropy (hunter2hunter2) and it sits inside one long word, so
// neither the prefix nor the entropy check could see it.
var urlPassword = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://[^\s/:@]+:)([^\s/@]+)@`)

// mysqlPassword is mysql's -p<password>, which takes the password glued to
// the flag. Only on a mysql-family command line: elsewhere -print and -prune
// are ordinary flags.
var (
	mysqlCommand  = regexp.MustCompile(`\b(mysql|mysqldump|mysqladmin|mariadb)\b`)
	mysqlPassword = regexp.MustCompile(`(^|\s)-p(\S+)`)
)

// privateKeyBlock is a whole PEM private key. Matched across lines and before
// anything else: masking only the BEGIN line would leave the base64 body, which
// is the key, on the lines under it — and in Mask nothing else would catch it.
// An unterminated block (a paste cut short) is masked to the end of the text.
var privateKeyBlock = regexp.MustCompile(`-----BEGIN (?:RSA |EC |DSA |OPENSSH |PGP |ENCRYPTED )?PRIVATE KEY(?: BLOCK)?-----(?s:.*?)(?:-----END [A-Z ]*PRIVATE KEY(?: BLOCK)?-----|\z)`)

// These contain a separator the token pass splits on (a colon, a URL's
// slashes), so they are matched on the whole line instead. The AWS secret
// access key has no prefix at all; only the name beside it identifies it.
var lineShapes = []struct {
	reason string
	re     *regexp.Regexp
	repl   string
}{
	{"Slack webhook URL", regexp.MustCompile(`https://hooks\.slack\.com/services/T[A-Za-z0-9_]{8,}/B[A-Za-z0-9_]{8,}/[A-Za-z0-9_]{20,}`), Marker},
	{"Telegram bot token", regexp.MustCompile(`\b[0-9]{8,10}:[A-Za-z0-9_-]{35}\b`), Marker},
	{"AWS secret access key", regexp.MustCompile(`(?i)(aws_?secret_?(?:access_?)?key["']?\s*[:=]\s*["']?)[A-Za-z0-9/+=]{40}`), "${1}" + Marker},
}

// secretPrefixes are provider-issued token shapes specific enough that
// matching on the prefix alone is safe: nothing else legitimately starts this
// way in command output or a distilled claim.
var secretPrefixes = []string{
	"sk-", "sk_live_", "sk_test_", "rk_live_", // OpenAI / Stripe secret keys
	"ghp_", "gho_", "ghu_", "ghs_", "ghr_", "github_pat_", // GitHub tokens
	"glpat-",       // GitLab personal access tokens
	"AKIA", "ASIA", // AWS access key IDs
	"xox", // Slack tokens (xoxb-, xoxp-, xoxa-, ...)
}

// tokenShapes are credential formats whose prefix alone is too common to
// trust — npm_config_cache, key-value, hf_ in a variable name — so each also
// pins the body's length and alphabet, and must match the whole token.
// Adapted from Hindsight's memory defense (vectorize-io/hindsight, MIT).
var tokenShapes = []struct {
	reason string
	re     *regexp.Regexp
}{
	{"Google API key", regexp.MustCompile(`^AIza[0-9A-Za-z_-]{35}$`)},
	{"Google OAuth token", regexp.MustCompile(`^ya29\.[0-9A-Za-z_-]{20,}$`)},
	{"xAI key", regexp.MustCompile(`^xai-[A-Za-z0-9]{40,}$`)},
	{"Groq key", regexp.MustCompile(`^gsk_[A-Za-z0-9]{20,}$`)},
	{"Hugging Face token", regexp.MustCompile(`^hf_[A-Za-z0-9]{30,}$`)},
	{"Replicate token", regexp.MustCompile(`^r8_[A-Za-z0-9]{30,}$`)},
	{"Perplexity key", regexp.MustCompile(`^pplx-[A-Za-z0-9]{40,}$`)},
	{"Databricks token", regexp.MustCompile(`^dapi[A-Za-z0-9]{32}$`)},
	{"DigitalOcean token", regexp.MustCompile(`^dop_v1_[a-f0-9]{64}$`)},
	{"npm token", regexp.MustCompile(`^npm_[A-Za-z0-9]{30,}$`)},
	{"PyPI token", regexp.MustCompile(`^pypi-AgEIcHlwaS5vcmc[A-Za-z0-9_-]{20,}$`)},
	{"Square token", regexp.MustCompile(`^sq0[a-z]{3}-[A-Za-z0-9_-]{22,}$`)},
	{"SendGrid key", regexp.MustCompile(`^SG\.[A-Za-z0-9_-]{22}\.[A-Za-z0-9_-]{43}$`)},
	{"Shopify token", regexp.MustCompile(`^shpat_[a-fA-F0-9]{32}$`)},
	{"Mailgun key", regexp.MustCompile(`^key-[A-Za-z0-9]{32}$`)},
	{"Discord bot token", regexp.MustCompile(`^[MNO][A-Za-z0-9]{23}\.[A-Za-z0-9_-]{6}\.[A-Za-z0-9_-]{27}$`)},
	{"JSON Web Token", regexp.MustCompile(`^eyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}$`)},
}

func mask(field, s string, entropy bool) (string, []Redaction) {
	if s == "" {
		return s, nil
	}
	var found []Redaction
	s = replaceCounting(privateKeyBlock, s, Marker, field, "private key", &found, nil)
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if !entropy {
			line = replaceCounting(authSchemed, line, "Authorization: "+Marker, field, "Authorization header", &found, func(m []string) bool {
				return !plainWord.MatchString(m[1])
			})
		} else if m := authHeaderLine.FindStringSubmatch(line); m != nil && !strings.Contains(m[0], Marker) {
			lines[i] = m[1] + "Authorization: " + Marker
			found = append(found, Redaction{Field: field, Reason: "Authorization header"})
			continue
		} else {
			line = replaceCounting(authHeaderInline, line, "Authorization: "+Marker, field, "Authorization header", &found, nil)
		}
		line = replaceCounting(urlPassword, line, "${1}"+Marker+"@", field, "password in a URL", &found, nil)
		if mysqlCommand.MatchString(line) {
			line = replaceCounting(mysqlPassword, line, "${1}-p"+Marker, field, "mysql -p password", &found, nil)
		}
		for _, ls := range lineShapes {
			line = replaceCounting(ls.re, line, ls.repl, field, ls.reason, &found, nil)
		}
		lines[i], found = redactTokens(field, line, entropy, found)
	}
	return strings.Join(lines, "\n"), found
}

// replaceCounting masks and counts each match of re that keep accepts (nil
// accepts all). A match that already holds the marker is left alone: a
// checkpoint masks its folded notes a second time, and "user:[REDACTED]@"
// matched the URL-password rule again and was reported as another secret.
func replaceCounting(re *regexp.Regexp, line, repl, field, reason string, found *[]Redaction, keep func([]string) bool) string {
	return re.ReplaceAllStringFunc(line, func(m string) string {
		if strings.Contains(m, Marker) {
			return m
		}
		if keep != nil && !keep(re.FindStringSubmatch(m)) {
			return m
		}
		*found = append(*found, Redaction{Field: field, Reason: reason})
		return re.ReplaceAllString(m, repl)
	})
}

// redactTokens walks whitespace-separated tokens in one line and masks any
// part of one that looks like a credential, preserving the text around it so
// the masked line still reads. A word is split on assignment and quoting
// punctuation first: whole-word matching missed --token=ghp_… and
// {"api_key":"sk-…"}, where the credential is only part of the word.
func redactTokens(field, line string, entropy bool, found []Redaction) (string, []Redaction) {
	words := strings.Fields(line)
	changed := false
	for i, w := range words {
		// Rebuilt part by part rather than with strings.Replace, which would
		// mask the first occurrence of the text — possibly inside an earlier,
		// harmless part — and leave the credential itself in place.
		var b strings.Builder
		start := -1
		flush := func(end int) {
			if start < 0 {
				return
			}
			part := w[start:end]
			if reason := secretReason(part, entropy); reason != "" {
				b.WriteString(Marker)
				found = append(found, Redaction{Field: field, Reason: reason})
				changed = true
			} else {
				b.WriteString(part)
			}
			start = -1
		}
		for j, r := range w {
			if isTokenSeparator(r) {
				flush(j)
				b.WriteRune(r)
			} else if start < 0 {
				start = j
			}
		}
		flush(len(w))
		words[i] = b.String()
	}
	if !changed {
		return line, found
	}
	return strings.Join(words, " "), found
}

func isTokenSeparator(r rune) bool {
	return strings.ContainsRune("=:@,;'\"()[]{}`", r)
}

// secretReason reports why tok looks like a credential, or "" if it does not.
func secretReason(tok string, entropy bool) string {
	// A marker an earlier pass left, split out of its brackets.
	if tok == "" || tok == strings.Trim(Marker, "[]") {
		return ""
	}
	for _, p := range secretPrefixes {
		if strings.HasPrefix(tok, p) && len(tok) >= len(p)+6 {
			return "known credential prefix (" + p + ")"
		}
	}
	// A sentence's full stop is not a separator — it sits inside JWTs and
	// hostnames — but it would stop an anchored shape from matching the key it
	// ends: "the token is hf_….".
	bare := strings.TrimRight(tok, ".!?")
	for _, ts := range tokenShapes {
		if ts.re.MatchString(bare) {
			return "known credential shape (" + ts.reason + ")"
		}
	}
	if !entropy {
		return ""
	}
	// After a scheme's colon, a URL's path is one long mixed-case word with
	// slashes in it — a GitHub link to a repository, say — which the entropy
	// check would flag. Only a known prefix is trusted there.
	if strings.HasPrefix(tok, "//") {
		return ""
	}
	// A filesystem path is not a credential, and no provider's token starts
	// with a path separator. Without this, `cd /Users/me/IdeaProjects/Thing`
	// harvested as `cd [REDACTED]`: on a real machine's Cursor history every
	// one of the 99 redactions was a path or a Java class name and none was a
	// secret. That is worse than useless twice over — it throws away the one
	// fact that says where a command ran, and it reports secrets to a user who
	// has none, which is what teaches them to ignore the report that matters.
	//
	// Entropy cannot make this call: measured, paths score 4.05-4.16 while a
	// 32-char hex key scores 3.93 and a JWT 4.36, so the ranges interleave and
	// no threshold separates them. The leading separator is structural, so it
	// can.
	if isPathPrefixed(tok) {
		return ""
	}
	// #210: harvested `go test -run TestWorkingNotesSurviveDeletingTheIndex`
	// became `go test -run [REDACTED]` and was counted as a secret found, as
	// was `go doc github.com/Coder8124/logos/...`. Both clear the entropy bar;
	// neither has a credential's structure. What they have instead is again
	// structural, like the path case, and asked before entropy for that reason.
	if isHostPath(tok) || isWordIdentifier(tok) {
		return ""
	}
	if looksHighEntropy(tok) {
		return "high-entropy token"
	}
	return ""
}

// isPathPrefixed reports whether a token opens with something only a
// filesystem path opens with. Deliberately narrow: it asks about the start of
// the token, not its shape, because a bare relative path and a base64 secret
// are genuinely hard to tell apart — an AWS secret access key such as
// wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY splits into short word-shaped
// segments exactly as src/main/java/com does. Those stay flagged; missing a
// path is cheap, missing a key is not.
func isPathPrefixed(tok string) bool {
	for _, p := range []string{"/", "./", "../", "~/"} {
		if strings.HasPrefix(tok, p) {
			return true
		}
	}
	return false
}

// hostPath is a module path or scheme-less URL: a lowercase dotted host, then
// a slash. No token alphabet opens that way — a JWT's and base64's first
// segment is mixed case, and a key has no dot before its first slash.
var hostPath = regexp.MustCompile(`^[a-z0-9-]+(\.[a-z0-9-]+)+/`)

func isHostPath(tok string) bool { return hostPath.MatchString(tok) }

// isWordIdentifier reports whether tok is a name built from words — a Go test
// name, a Java class, a dotted package — rather than random characters.
//
// Split at every capital and separator, a name's parts average well over four
// letters (TestAVaultThat… is 4.7) and each long one has a vowel in it. Random
// mixed-case letters split into parts of one or two, since every other
// character is a capital, and a long run of random lowercase often has no
// vowel. A digit anywhere means "not a name": almost every real key has one,
// and a name almost never does. Measured on random keys that clear the
// entropy bar, this lets through none of 32 characters or more over base62,
// 0.02% of 20-character ones, and 0.55% of 20-character letters-only ones —
// the known prefixes and shapes, asked first, are what catch a real key.
func isWordIdentifier(tok string) bool {
	var parts []string
	begin := 0
	for i, r := range tok {
		switch {
		case r >= 'A' && r <= 'Z':
			// Every capital opens a part, runs of them included: merged, "AV"
			// in TestAVault would let random letters pass as words too.
			parts = append(parts, tok[begin:i])
			begin = i
		case r >= 'a' && r <= 'z':
		case r == '.' || r == '_' || r == '-' || r == '/':
			parts = append(parts, tok[begin:i])
			begin = i + 1
		default:
			return false
		}
	}
	parts = append(parts, tok[begin:])
	letters, n := 0, 0
	for _, p := range parts {
		if p == "" {
			continue
		}
		if len(p) > 3 && !strings.ContainsAny(strings.ToLower(p), "aeiouy") {
			return false
		}
		letters += len(p)
		n++
	}
	return n > 0 && letters >= 4*n
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
