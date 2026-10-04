package contextpack

import (
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/session"
)

// The italic lines are Logos talking about the pack rather than the pack
// itself, and they arrive in every resume. Written as paragraphs they were a
// third of a small pack's footer; one short line each says the same.
const maxFraming = 100

func TestThePacksOwnLinesAreOneShortLineEach(t *testing.T) {
	ix := seedVault(t)
	tight, _ := Build(ix, nil, "", Request{Task: "reduce cost", Hint: "kestrel-one", Budget: 40})

	now := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)
	empty := &Pack{
		Task:    "what did I do about two years ago?",
		Working: []session.Note{{Text: "Ran the drop test series.", TS: now.AddDate(0, 0, -3).Unix()}},
	}
	empty.Budget.Limit = DefaultBudget
	empty.applyWindow(Request{Task: empty.Task, Now: now.Unix()})

	for _, out := range []string{tight.Render(), empty.Render()} {
		for _, line := range strings.Split(out, "\n") {
			// The budget line's length is its per-section breakdown, which is data.
			if !strings.HasPrefix(line, "_") || strings.HasPrefix(line, "_Context budget") {
				continue
			}
			if n := len([]rune(line)); n > maxFraming {
				t.Errorf("framing line is %d characters, want at most %d:\n%s", n, maxFraming, line)
			}
		}
	}
}
