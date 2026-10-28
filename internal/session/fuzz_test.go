package session

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/untrusted"
)

// A checkpoint file is the one piece of the vault most likely to be opened in
// an editor and saved back by hand, and ParseCheckpoint is forgiving on purpose
// so a hand edit costs nothing it did not touch. Forgiving parsers are where a
// panic or a silent rewrite hides, so this throws hand-edited shapes at it:
//
//	go test ./internal/session -run '^$' -fuzz FuzzParseCheckpoint -fuzztime 60s
//
// The properties:
//   - it never panics, on anything;
//   - what it reads, written back and read again, is what it read — the
//     property Latest and a rebuilt index both stand on, since each re-reads
//     what an earlier write rendered;
//   - and a file it cannot make sense of reads as Empty, which is the signal
//     History and Latest skip on, never as a checkpoint with blank fields
//     that looks like a real one.
func FuzzParseCheckpoint(f *testing.F) {
	full := Checkpoint{
		Session: "20261028-101500-ab12", Project: "kestrel", Agent: "claude-code",
		HostSession: "c0ffee", Task: "wire the parser", Intent: "so resume reads it",
		State:     "half done\n\n## not a heading",
		Decisions: []string{"yaml over toml, because the vault is yaml"},
		Failed:    []string{"route: a regex | observation: it missed CRLF"},
		Verified:  []string{"go test ./internal/session"},
		Next:      "fuzz it", TS: 1761646500, Turns: 3,
	}
	seeds := []string{
		full.Markdown(""),
		full.Markdown("20261027-090000-cd34"),
		// Missing frontmatter: the body alone, as someone pasting a section.
		"## Task\n\nwire the parser\n\n## Next\n\nfuzz it\n",
		// Duplicated keys — yaml.v3 refuses the whole mapping.
		"---\ntype: checkpoint\nproject: a\nproject: b\n---\n\n## Next\n\nx\n",
		// Windows line endings and a byte-order mark, as Notepad saves.
		strings.ReplaceAll(full.Markdown(""), "\n", "\r\n"),
		"\ufeff" + full.Markdown(""),
		// Unterminated frontmatter fence and an unterminated code fence.
		"---\ntype: checkpoint\nproject: kestrel\n\n## Next\n\nx\n",
		"---\nproject: kestrel\n---\n\n## State\n\n```go\nfunc x() {\n## Next\n\ny\n",
		// A giant line and bytes that are not UTF-8.
		"---\nproject: kestrel\n---\n\n## Task\n\n" + strings.Repeat("long ", 20000) + "\n",
		"---\nproject: \xff\xfekestrel\n---\n\n## Next\n\n\xc3\x28 bad\n",
		// A project a hand edit gave a quote and a colon.
		"---\nproject: \"ke\\\"strel: x\"\n---\n\n## Next\n\ny\n",
		"",
		"---\n---\n",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		c1 := ParseCheckpoint(raw)
		if c1.Empty() {
			return // reads as unreadable, which is what every caller skips on
		}
		md := c1.Markdown("")
		c2 := ParseCheckpoint(md)
		if c2.Empty() {
			t.Fatalf("a checkpoint that parsed was written back as one that reads empty\nread: %#v\nwrote:\n%s", c1, md)
		}
		want := asWritten(c1)
		if !reflect.DeepEqual(want, c2) {
			t.Fatalf("parse -> write -> parse changed the checkpoint\nfirst:  %#v\nsecond: %#v\nwrote:\n%s", want, c2, md)
		}
		// And the written form is a fixed point: rendering what it read back
		// produces the same bytes, so repeated rewrites cannot drift.
		if again := c2.Markdown(""); again != md {
			t.Fatalf("writing the re-read checkpoint produced different bytes\nfirst:\n%s\nsecond:\n%s", md, again)
		}
		// The same file after an editor on Windows has saved it — CRLF line
		// endings, a byte-order mark — is the same checkpoint. Reading it as
		// one with no frontmatter keeps the sections and drops the project,
		// session and time without a word, which is a success-shaped loss.
		for name, edited := range map[string]string{
			"CRLF line endings": strings.ReplaceAll(md, "\n", "\r\n"),
			"a byte-order mark": "\ufeff" + md,
		} {
			if got := ParseCheckpoint(edited); !reflect.DeepEqual(got, c2) {
				t.Fatalf("the written checkpoint saved with %s reads differently\nwant: %#v\ngot:  %#v", name, c2, got)
			}
		}
	})
}

// asWritten applies the normalisation Markdown documents — free text trimmed,
// a bullet collapsed to one line, empty bullets dropped — so the round trip
// compares what the writer promises to keep, not what it promises to tidy.
func asWritten(c Checkpoint) Checkpoint {
	trim := func(s string) string { return strings.TrimSpace(s) }
	list := func(in []string) []string {
		var out []string
		for _, s := range in {
			if s = untrusted.Inline(s); s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	c.Task, c.Intent, c.State, c.Next = trim(c.Task), trim(c.Intent), trim(c.State), trim(c.Next)
	c.Decisions, c.Failed, c.Verified, c.Unseen = list(c.Decisions), list(c.Failed), list(c.Verified), list(c.Unseen)
	c.Blockers, c.Commands, c.Questions, c.Files = list(c.Blockers), list(c.Commands), list(c.Questions), list(c.Files)
	if in := c.Inferred; in != nil {
		cp := *in
		cp.Verified, cp.Failed, cp.Decided, cp.Wanted = list(in.Verified), list(in.Failed), list(in.Decided), list(in.Wanted)
		c.Inferred = &cp
	}
	return c
}
