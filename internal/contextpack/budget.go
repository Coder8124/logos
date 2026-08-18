package contextpack

import "strings"

// Token budgeting.
//
// The goal is not maximum retrieval, it is maximum useful context per token.
// A pack that returns everything relevant is easy and useless — it either
// overflows the window or crowds out the conversation. So each section gets a
// share of the budget, is filled by rank until that share is gone, and reports
// what it had to leave behind.
//
// Shares are ordered by what a resuming agent actually needs first. The
// checkpoint outranks everything: knowing where the last agent stopped and what
// they ruled out is worth more than any amount of background, and it is also
// the cheapest thing here. Vault prose gets the largest share because it is the
// only section carrying real content rather than one-line summaries.
//
// Unspent allowance carries forward rather than evaporating, so a project with
// no checkpoint yet spends that budget on notes instead of returning a thinner
// pack than it was asked for.

// DefaultBudget is roughly a page and a half of context — enough to be useful,
// small enough that a host can afford to call this on every task.
const DefaultBudget = 4000

type Budget struct {
	Limit int    `json:"limit"`
	Spent int    `json:"spent"`
	By    []Line `json:"by"`
}

// A Line is one section's spend, including what it could not fit.
type Line struct {
	Section string `json:"section"`
	Tokens  int    `json:"tokens"`
	Items   int    `json:"items"`
	Dropped int    `json:"dropped"`
}

// Section names, in render order. Also the keys into shares.
const (
	secCheckpoint = "checkpoint"
	secWorking    = "working notes"
	secProject    = "project"
	secNotes      = "vault notes"
	secMemories   = "memories"
	secLoops      = "open loops"
)

var order = []string{secCheckpoint, secWorking, secProject, secNotes, secMemories, secLoops}

var shares = map[string]float64{
	secCheckpoint: 0.18,
	secWorking:    0.07,
	secProject:    0.15,
	secNotes:      0.35,
	secMemories:   0.17,
	secLoops:      0.08,
}

// estimate approximates tokens from characters. Four characters per token is
// the usual rule of thumb for English prose and is close enough for deciding
// what to drop; a real tokenizer would be a dependency and a model-specific
// answer to a question that only needs to be roughly right.
func estimate(s string) int {
	return len(s)/4 + 1
}

// spender doles out one section's allowance, carrying anything unspent forward
// to the sections that follow.
type spender struct {
	limit int
	carry int
	spent int
	lines []Line
}

func newSpender(limit int) *spender { return &spender{limit: limit} }

// allowance returns how much this section may spend: its share of the total
// plus everything earlier sections left on the table.
func (s *spender) allowance(section string) int {
	return int(shares[section]*float64(s.limit)) + s.carry
}

// take fills a section. want is the ordered list of candidate chunks; it returns
// the ones that fit. The first item is always admitted even if it exceeds the
// allowance — a section that appears in the output with nothing under it is
// more confusing than one slightly over budget.
func (s *spender) take(section string, want []string) []string {
	budget := s.allowance(section)
	var kept []string
	used := 0
	for _, chunk := range want {
		cost := estimate(chunk)
		if used+cost > budget && len(kept) > 0 {
			break
		}
		kept = append(kept, chunk)
		used += cost
	}

	s.carry = budget - used
	if s.carry < 0 {
		s.carry = 0
	}
	s.spent += used
	if len(kept) > 0 || len(want) > 0 {
		s.lines = append(s.lines, Line{
			Section: section, Tokens: used, Items: len(kept), Dropped: len(want) - len(kept),
		})
	}
	return kept
}

// fit trims a single blob to an allowance, cutting on a line boundary so a
// truncated checkpoint still ends mid-thought rather than mid-word.
func fit(text string, budget int) (string, bool) {
	if estimate(text) <= budget {
		return text, false
	}
	lines := strings.Split(text, "\n")
	var b strings.Builder
	for _, l := range lines {
		if estimate(b.String())+estimate(l) > budget {
			break
		}
		b.WriteString(l)
		b.WriteString("\n")
	}
	out := strings.TrimRight(b.String(), "\n")
	if out == "" { // one very long line: hard cut rather than return nothing
		out = text[:min(len(text), budget*4)]
	}
	return out + "\n\n_[trimmed to fit the budget]_", true
}
