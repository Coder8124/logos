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

func sections(body string) map[string]string {
	out := map[string]string{}
	var heading string
	var buf []string
	flush := func() {
		if heading != "" {
			out[heading] = strings.TrimSpace(strings.Join(buf, "\n"))
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
