package secret

import (
	"regexp"
	"strings"
)

// Personal data and credential-bearing headers, masked in both strengths. They
// are not provider token shapes — a session cookie or an X-API-Key value has
// no prefix to match — so what identifies them is the structure around them: a
// header's name, an address's "@", a phone number's separators.
//
// Each is deliberately narrow, because a transcript is full of text that
// resembles them: git@github.com remotes, name@1.2.3 package versions, dates
// and build numbers that are digits joined by hyphens.

var emailAddress = regexp.MustCompile(`[A-Za-z0-9][A-Za-z0-9._%+-]*@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}\b`)

// sshUser finds an ssh-family command earlier on the line: "ssh deploy@host" and
// "scp a.tar deploy@host:/srv" name a login, not somebody's address.
var sshUser = regexp.MustCompile(`\b(?:ssh|scp|sftp|rsync)\b[^;&|\n]*$`)

// versionLike is a local part that is a package name and a version, not a
// person: react@18.3.0-canary.beta.
var versionLike = regexp.MustCompile(`^[A-Za-z0-9._%+-]*@\d`)

// phoneNumber has two shapes: a country-coded number (+44 20 7946 0958), and a
// North American one that is dash- or parenthesis-separated ((415) 555-0132,
// 415-555-0132). Spaces alone ("128 256 1024", "ports 443 808 8080") and dots
// ("100.200.3000") are never enough: they are sizes, ports and versions. A bare run of
// digits is never a phone number here: it is as likely an id or a timestamp,
// and masking those would teach people to ignore the report that matters.
var phoneNumber = regexp.MustCompile(`(^|[^\w.+-])(\+[1-9]\d{0,2}[ -]\(?\d{1,4}\)?(?:[ -]\(?\d{1,4}\)?){1,4}|\(\d{3}\)[ -]?\d{3}-\d{4}|\d{3}-\d{3}-\d{4})\b`)

// credentialHeaders: the value of these is the credential, as with
// Authorization. A cookie header is a list of name=value pairs, so it is only
// masked when it looks like one — "Cookie: sessions are httpOnly" is prose.
var (
	cookieHeader = regexp.MustCompile(`(?i)\b(?:Set-)?Cookie\s*:\s*([A-Za-z0-9_.-]+=[^\s;'"]*(?:;[ \t]*[A-Za-z0-9_.-]+(?:=[^\s;'"]*)?)*;?)`)
	keyHeader    = regexp.MustCompile(`(?i)\b(?:X-API-Key|X-Api-Token|X-Auth-Token|X-Access-Token|X-CSRF-Token|X-XSRF-Token|X-Amz-Security-Token|X-Goog-Api-Key|Proxy-Authorization|Api-Key|Apikey)\s*:\s*(?:(?:Bearer|Basic|Token|Digest)\s+)?([^\s'",;]{8,})`)
)

// maskPersonal masks emails, phone numbers and credential headers in one line.
func maskPersonal(line string, hits *hitSet, field string, found *[]Redaction) string {
	offset := len(tokenRules)
	if hits.has(offset + hitCookie) {
		line = replaceGroup(cookieHeader, line, field, "Cookie header", found, nil)
	}
	if hits.has(offset + hitKeyHeader) {
		line = replaceGroup(keyHeader, line, field, "credential header", found, func(v string) bool {
			// "X-API-Key: required" names a header in prose, and
			// "$OPENAI_API_KEY" or "<your-key>" is a placeholder, not a value.
			return !plainWord.MatchString(v) && !strings.HasPrefix(v, "$") && !strings.HasPrefix(v, "<") &&
				!strings.HasPrefix(v, "{{") && !strings.HasPrefix(v, "%")
		})
	}
	if strings.Contains(line, "@") {
		line = replaceWhole(emailAddress, line, field, "email address", found, func(line string, s, e int) bool {
			// A URL's user name, the git user of an ssh remote and a login after
			// ssh are not somebody's address; git@company.com in prose is.
			addr := line[s:e]
			switch {
			case strings.HasSuffix(line[:s], "://"):
				return false
			case strings.HasPrefix(addr, "git@") && e < len(line) && line[e] == ':':
				return false
			case versionLike.MatchString(addr), sshUser.MatchString(line[:s]):
				return false
			case strings.IndexByte(addr, '@') < 2:
				// "x@self.weights": a one-character local part is a decorator or a handle.
				return false
			}
			host := addr[strings.IndexByte(addr, '@')+1:]
			for _, label := range strings.Split(host, ".") {
				if label != "" && strings.Trim(label, "0123456789") == "" {
					return false
				}
			}
			return true
		})
	}
	if mayHavePhone(line) {
		line = replaceGroup(phoneNumber, line, field, "phone number", found, func(v string) bool {
			n := 0
			for _, r := range v {
				if r >= '0' && r <= '9' {
					n++
				}
			}
			return n >= 8 && n <= 15
		}, 2)
	}
	return line
}

// replaceGroup masks capture `group` (default 1) of each match that keep accepts.
func replaceGroup(re *regexp.Regexp, line, field, reason string, found *[]Redaction, keep func(string) bool, group ...int) string {
	g := 1
	if len(group) > 0 {
		g = group[0]
	}
	var b strings.Builder
	last := 0
	for _, m := range re.FindAllStringSubmatchIndex(line, -1) {
		s, e := m[2*g], m[2*g+1]
		if s < last || strings.Contains(line[s:e], Marker) || (keep != nil && !keep(line[s:e])) {
			continue
		}
		b.WriteString(line[last:s])
		b.WriteString(Marker)
		last = e
		*found = append(*found, Redaction{Field: field, Reason: reason})
	}
	if last == 0 {
		return line
	}
	b.WriteString(line[last:])
	return b.String()
}

// replaceWhole masks each whole match that keep accepts, given where it sits.
func replaceWhole(re *regexp.Regexp, line, field, reason string, found *[]Redaction, keep func(line string, s, e int) bool) string {
	var b strings.Builder
	last := 0
	for _, m := range re.FindAllStringIndex(line, -1) {
		if m[0] < last || !keep(line, m[0], m[1]) {
			continue
		}
		b.WriteString(line[last:m[0]])
		b.WriteString(Marker)
		last = m[1]
		*found = append(*found, Redaction{Field: field, Reason: reason})
	}
	if last == 0 {
		return line
	}
	b.WriteString(line[last:])
	return b.String()
}

// mayHavePhone is a cheap necessary condition for phoneNumber: a "+" before a
// digit, a parenthesised area code, or three digits then a separator then
// three more. The regexp opens with a group and cannot skip ahead, so without
// this it walked every line that held a digit and a space.
func mayHavePhone(line string) bool {
	isDigit := func(c byte) bool { return c >= '0' && c <= '9' }
	for i := 0; i < len(line); i++ {
		switch c := line[i]; {
		case c == '+' && i+1 < len(line) && isDigit(line[i+1]):
			return true
		case c == '(' && i+4 < len(line) && isDigit(line[i+1]) && isDigit(line[i+2]) && isDigit(line[i+3]) && line[i+4] == ')':
			return true
		case c == '-' && i >= 3 && i+3 < len(line) &&
			isDigit(line[i-1]) && isDigit(line[i-2]) && isDigit(line[i-3]) &&
			isDigit(line[i+1]) && isDigit(line[i+2]) && isDigit(line[i+3]):
			return true
		}
	}
	return false
}
