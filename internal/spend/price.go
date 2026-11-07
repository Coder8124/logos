package spend

import "strings"

// PriceSource is shown beside every dollar figure. The table is built in and
// never fetched: nothing leaves the machine, and a price looked up on each run
// would be a network call for a number that changes a few times a year. The
// date says how old it is, so a reader can tell a stale table from a cheap
// month.
const PriceSource = "Anthropic list prices as of 6 Oct 2026, standard tier, before any discount"

// Price is a model's list price in dollars per million tokens. Cache writes
// are priced from Input by the published multipliers — 1.25× for the 5-minute
// cache, 2× for the 1-hour one — so only the read rate, which differs by
// model, is listed.
type Price struct {
	Input, Output, CacheRead float64
	// Long is the rate card for a request whose prompt passes LongAfter
	// tokens, for a model with two (Haiku 5.5). Zero when there is one card.
	Long      *Price
	LongAfter int64
}

// prices holds only what was read from a published source. A model missing
// here is shown with its tokens and "no list price", never with a guess: a
// dashboard that invents a figure is worse than one that leaves it blank.
var prices = map[string]Price{
	"claude-fable-5-1":  {Input: 10, Output: 50, CacheRead: 0.25},
	"claude-opus-5-5":   {Input: 4, Output: 20, CacheRead: 0.20},
	"claude-opus-5":     {Input: 5, Output: 25, CacheRead: 0.50},
	"claude-sonnet-5-5": {Input: 2, Output: 10, CacheRead: 0.20},
	"claude-sonnet-5":   {Input: 2, Output: 10, CacheRead: 0.20},
	"claude-haiku-5-5": {Input: 0.10, Output: 0.50, CacheRead: 0.01,
		Long: &Price{Input: 0.50, Output: 2.50, CacheRead: 0.05}, LongAfter: 100_000},
}

// Cost prices one request. ok is false for a model with no list price.
func Cost(model string, u Usage) (usd float64, ok bool) {
	p, ok := prices[baseModel(model)]
	if !ok {
		return 0, false
	}
	if p.Long != nil && u.Prompt() > p.LongAfter {
		p = *p.Long
	}
	return (float64(u.Input)*p.Input +
		float64(u.CacheWrite5m)*p.Input*1.25 +
		float64(u.CacheWrite1h)*p.Input*2 +
		float64(u.CacheRead)*p.CacheRead +
		float64(u.Output)*p.Output) / 1e6, true
}

// baseModel drops a context-size tag such as "[1m]", which a host may append
// to the id it records; the price is the model's either way.
func baseModel(m string) string {
	if i := strings.IndexByte(m, '['); i > 0 {
		m = m[:i]
	}
	return strings.TrimSpace(m)
}
