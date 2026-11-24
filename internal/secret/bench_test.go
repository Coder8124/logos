package secret

import (
	"strings"
	"testing"
)

// transcriptBlocks builds about 5 MB of transcript-like text, in blocks the
// size of one decoded JSON string value: ordinary lines of code, logs and
// prose with a token in one block of fifty.
func transcriptBlocks(t testing.TB) []string {
	ord := corpusLines2(t, "ordinary.txt")
	toks := corpusLines2(t, "tokens.txt")
	filler := []string{
		"func (s *Store) Load(ctx context.Context, id string) (Record, error) { return s.db.Get(ctx, id) }",
		"2026-11-22T10:14:03Z INFO request handled path=/api/v1/projects/brain status=200 dur=12.4ms",
		"    if err != nil { return fmt.Errorf(\"read %s: %w\", path, err) }",
		"The ingest pass keeps the vault as the source of truth and rebuilds the index from it on demand.",
	}
	var blocks []string
	total, n := 0, 0
	for total < 5<<20 {
		var b strings.Builder
		for b.Len() < 2048 {
			if n%7 == 0 {
				b.WriteString(ord[n/7%len(ord)])
			} else {
				b.WriteString(filler[n%len(filler)])
			}
			b.WriteByte('\n')
			n++
		}
		if len(blocks)%50 == 49 {
			b.WriteString("export TOKEN=" + toks[len(blocks)/50%len(toks)] + "\n")
		}
		blocks = append(blocks, b.String())
		total += b.Len()
	}
	return blocks
}

func corpusLines2(t testing.TB, name string) []string {
	tt, _ := t.(*testing.T)
	_ = tt
	var out []string
	for _, c := range readCorpus(t, name) {
		out = append(out, c[1])
	}
	return out
}

func benchMask(b *testing.B, mask func(string, string) (string, []Redaction)) {
	blocks := transcriptBlocks(b)
	size := 0
	for _, s := range blocks {
		size += len(s)
	}
	b.SetBytes(int64(size))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, s := range blocks {
			mask("t", s)
		}
	}
}

func BenchmarkMaskAllFiveMegabytesOfTranscript(b *testing.B) { benchMask(b, MaskAll) }
func BenchmarkMaskFiveMegabytesOfTranscript(b *testing.B)    { benchMask(b, Mask) }

// repeatedBlocks fills ~5 MB with 2 KB blocks of the given lines, to cost the
// keyword prefilter on text that is all one kind: prose that mentions no
// provider, and lines dense in short words that are also rule keywords.
func repeatedBlocks(lines []string) []string {
	var blocks []string
	total, n := 0, 0
	for total < 5<<20 {
		var b strings.Builder
		for b.Len() < 2048 {
			b.WriteString(lines[n%len(lines)])
			b.WriteByte('\n')
			n++
		}
		blocks = append(blocks, b.String())
		total += b.Len()
	}
	return blocks
}

func benchBlocks(b *testing.B, blocks []string, mask func(string, string) (string, []Redaction)) {
	size := 0
	for _, s := range blocks {
		size += len(s)
	}
	b.SetBytes(int64(size))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, s := range blocks {
			mask("t", s)
		}
	}
}

var proseLines = []string{
	"The ingest pass keeps the vault as the source of truth and rebuilds the index from it on demand.",
	"Nothing leaves the machine, so the report names what was skipped and how many of them there were.",
	"A checkpoint records what was verified, what failed and the first step the following agent should take.",
}

// keywordDenseLines are ordinary text full of the short words the prefilter
// keys on (task, key, they, access, api, sk, eyes, ado), the case where a
// short keyword makes every line pay for a regexp run.
var keywordDenseLines = []string{
	"the task key access api they asked for was a risk to the desk; key-value pairs and api-version headers",
	"tasks keys eyes tornado Colorado shadow disk risk access-control list, key-rotation, api-gateway, us-east-1",
	"they key the task: sk status, ey, ado, secret_name, discord, azure, twilio, heroku, airtable, devops notes",
}

func BenchmarkMaskAllProse(b *testing.B) { benchBlocks(b, repeatedBlocks(proseLines), MaskAll) }
func BenchmarkMaskProse(b *testing.B)    { benchBlocks(b, repeatedBlocks(proseLines), Mask) }
func BenchmarkMaskAllKeywordDenseLines(b *testing.B) {
	benchBlocks(b, repeatedBlocks(keywordDenseLines), MaskAll)
}
func BenchmarkMaskKeywordDenseLines(b *testing.B) {
	benchBlocks(b, repeatedBlocks(keywordDenseLines), Mask)
}
