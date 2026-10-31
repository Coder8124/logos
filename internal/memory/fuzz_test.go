package memory

import (
	"fmt"
	"strings"
	"testing"
)

// memories/<kind>.md and memories/<kind>/<uid>.md are the files a person is
// told to open and correct, and parseKind is what `logos index` rebuilds the
// whole store from after `rm -rf .logos`. A line it misreads is a memory
// changed; a line it drops is a memory Import then deletes from the cache.
//
//	go test ./internal/memory -run '^$' -fuzz FuzzParseKind -fuzztime 60s
//
// The properties:
//   - it never panics, on anything;
//   - what it reads, written back with renderLine and read again, is what it
//     read, field for field, and the written lines are a fixed point;
//   - the same file saved by a Windows editor — CRLF, a byte-order mark —
//     reads as the same memories.
//
// parseKind has no error to return: a line it cannot read is not a memory, and
// that is the contract the tolerant format promises. What it must never do is
// turn a line into a different memory, which is what the round trip checks.
func FuzzParseKind(f *testing.F) {
	line := renderLine(Memory{ID: 7, Text: "the waveguide costs 4.20 dollars", Confidence: 0.8,
		Salience: 0.6, Source: "mcp", Created: 1761646500, LastUsed: 1761650000, Uses: 2,
		Project: "billing api", Agent: "claude-code", Pin: PinAlways,
		Uid: strings.Repeat("ab", uidLen/2), Commit: "0123456789abcdef0123456789abcdef01234567"})
	file := "---\ntype: memory\nkind: fact\nuid: " + strings.Repeat("ab", uidLen/2) + "\n---\n\n" + line
	seeds := []string{
		file,
		renderKind(Fact, []Memory{{ID: 1, Text: "a", Created: 1}, {ID: 2, Text: "b", Created: 2}}),
		// Hand-typed: no metadata comment, no frontmatter.
		"- prefer terse replies\n- standup moved to 09:15\n",
		// Duplicated keys in the comment: the last one wins, as applyMeta reads.
		"- a fact <!-- logos id=3 id=4 conf=0.9 conf=0.1 -->\n",
		// CRLF and a byte-order mark, as Notepad saves.
		strings.ReplaceAll(file, "\n", "\r\n"),
		"\ufeff- a memory typed in Notepad\r\n",
		// An unterminated comment and an unterminated fence.
		"- torn line <!-- logos id=9 conf=0.\n",
		"```\n- inside a code fence\n",
		// The text quoting the comment syntax, and a project with escapes.
		"- use <!-- and --> in html <!-- logos id=5 project=a%20b%25c -->\n",
		// A giant line and bytes that are not UTF-8.
		"- " + strings.Repeat("long ", 20000) + "\n",
		"- \xff\xfe not utf-8 \xc3\x28 <!-- logos id=6 agent=\xff -->\n",
		// Values out of range or unparseable.
		"- odd <!-- logos id=x conf=NaN sal=1e999 uses=-3 pin=99 created=yesterday -->\n",
		"",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		first := parseKind(Fact, raw)
		var b strings.Builder
		for _, m := range first {
			b.WriteString(renderLine(m))
		}
		written := b.String()
		second := parseKind(Fact, written)
		if len(second) != len(first) {
			t.Fatalf("%d memories read, %d after writing them back\nwrote:\n%s", len(first), len(second), written)
		}
		for i := range first {
			if want, got := asWrittenLine(first[i]), asWrittenLine(second[i]); want != got {
				t.Fatalf("memory %d changed on the round trip\nfirst:  %s\nsecond: %s", i, want, got)
			}
		}
		var again strings.Builder
		for _, m := range second {
			again.WriteString(renderLine(m))
		}
		if again.String() != written {
			t.Fatalf("writing the re-read memories produced different lines\nfirst:\n%s\nsecond:\n%s", written, again.String())
		}
		for name, edited := range map[string]string{
			"CRLF line endings": strings.ReplaceAll(written, "\n", "\r\n"),
			"a byte-order mark": "\ufeff" + written,
		} {
			got := parseKind(Fact, edited)
			if len(got) != len(second) {
				t.Fatalf("the written memories saved with %s read as %d, not %d", name, len(got), len(second))
			}
			for i := range got {
				if asWrittenLine(got[i]) != asWrittenLine(second[i]) {
					t.Fatalf("memory %d saved with %s reads differently\nwant: %s\ngot:  %s", i, name, asWrittenLine(second[i]), asWrittenLine(got[i]))
				}
			}
		}
	})
}

// asWrittenLine is a memory as the vault format promises to keep it: the
// fields renderLine writes, with the text collapsed to one line and the two
// scores at the two decimals the file carries. Kind, turn and contests are
// left out on purpose — a kind file takes its kind from its name, and turn and
// contests belong to pending.md's format, not this one.
func asWrittenLine(m Memory) string {
	return fmt.Sprintf("text=%q id=%d conf=%.2f sal=%.2f src=%q created=%d used=%d uses=%d project=%q agent=%q pin=%d accepted=%d commit=%q uid=%q supersedes=%q",
		oneLine(m.Text), m.ID, m.Confidence, m.Salience, orDash(m.Source), m.Created, m.LastUsed, m.Uses,
		m.Project, strings.ReplaceAll(m.Agent, " ", "-"), m.Pin, m.AutoAccepted, m.Commit, m.Uid, m.Supersedes)
}
