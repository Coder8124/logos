package ingest

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// These mirror the small markdown helpers in internal/session: kept as local
// copies rather than exported, since the two packages share no other reason to
// depend on each other and the checkpoint's frontmatter dialect is its own.

func sec(b *strings.Builder, heading, body string) {
	body = strings.TrimSpace(body)
	if body == "" {
		return
	}
	fmt.Fprintf(b, "\n## %s\n\n%s\n", heading, body)
}

func bul(b *strings.Builder, heading string, items []string) {
	items = nonEmpty(items)
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(b, "\n## %s\n\n", heading)
	for _, it := range items {
		fmt.Fprintf(b, "- %s\n", strings.TrimSpace(it))
	}
}

func nonEmpty(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}

func ys(s string) string {
	b, err := yaml.Marshal(s)
	if err != nil {
		return `""`
	}
	return strings.TrimRight(string(b), "\n")
}

func orDefault(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

// safeSegment reduces a harness name, session id or project to something safe as
// one path segment: a directory-traversal component in a session id must not
// become a write outside the ingest folder.
func safeSegment(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		case r == ' ' || r == '/' || r == '\\':
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-.")
	if out == "" {
		return "x"
	}
	return out
}

func splitFM(raw string) (string, string) {
	if !strings.HasPrefix(raw, "---\n") {
		return "", raw
	}
	rest := raw[4:]
	i := strings.Index(rest, "\n---\n")
	if i < 0 {
		return "", raw
	}
	return rest[:i], strings.TrimLeft(rest[i+5:], "\n")
}

// section is one "## Heading" block of a candidate note.
type section struct {
	Heading string
	Text    string
}

// sections splits a note body into its "## Heading" blocks in document order.
// It used to return a map, which ranges nondeterministically: a note with two
// "## Verified" blocks would keep whichever the map happened to yield last, so
// the same file parsed to different candidates run to run. Order is preserved
// and same-heading blocks are merged (newline-joined) so every block survives.
func sections(body string) []section {
	var out []section
	idx := map[string]int{}
	var heading string
	var buf []string
	flush := func() {
		if heading != "" {
			text := strings.TrimSpace(strings.Join(buf, "\n"))
			if i, ok := idx[heading]; ok {
				out[i].Text = strings.TrimSpace(out[i].Text + "\n" + text)
			} else {
				idx[heading] = len(out)
				out = append(out, section{Heading: heading, Text: text})
			}
		}
		buf = buf[:0]
	}
	for _, line := range strings.Split(body, "\n") {
		if h, ok := strings.CutPrefix(line, "## "); ok {
			flush()
			heading = strings.TrimSpace(h)
			continue
		}
		buf = append(buf, line)
	}
	flush()
	return out
}

func parseBullets(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		for _, marker := range []string{"- ", "* ", "+ "} {
			if rest, ok := strings.CutPrefix(line, marker); ok {
				out = append(out, strings.TrimSpace(rest))
				break
			}
		}
	}
	return out
}
