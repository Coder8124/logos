package ops

import (
	"time"
)

// Block is the outcome block every --json answer carries (amp-spec §9.5): what
// was searched, how much came back, what was held back and why, and what went
// wrong. It exists because a JSON consumer cannot read the prose a text answer
// puts around its result, and the one reading that prose prevented — "nothing
// found" from "could not look" — is the one a list alone cannot make. An empty
// array is both.
type Block struct {
	Status   string     `json:"status"`
	Searched []Searched `json:"searched"`
	Returned int        `json:"returned"`
	Omitted  Omitted    `json:"omitted"`
	Window   *Window    `json:"window,omitempty"`
	Errors   []string   `json:"errors"`
	// Notes is what the text answer would have said beside the result — the
	// review queue's count, a sweep that saved a missing checkpoint. Kept,
	// because a feature that announces itself in text and goes quiet in JSON
	// is a feature the agents reading JSON never hear about.
	Notes []string `json:"notes,omitempty"`
}

const (
	StatusOK      = "ok"
	StatusEmpty   = "empty"
	StatusPartial = "partial"
	StatusFailed  = "failed"
)

// Searched is one scope a lookup read. AllProjects stands for every project at
// once, which no single name can say.
type Searched struct {
	Project     string `json:"project,omitempty"`
	AllProjects bool   `json:"all_projects,omitempty"`
}

// Omitted counts what matched and was not returned, by reason. Every key is
// always present, as the spec writes them, so a reader can sum them without
// first asking which ones this command knows about; a zero is "none held back
// for that reason here".
type Omitted struct {
	Budget        int `json:"budget"`
	Scope         int `json:"scope"`
	Quarantined   int `json:"quarantined"`
	Stale         int `json:"stale"`
	AnchorUnknown int `json:"anchor_unknown"`
}

// Window is the span of time a lookup was filtered to, and how many items it
// set aside for falling outside it.
type Window struct {
	From     string `json:"from"`
	To       string `json:"to"`
	SetAside int    `json:"set_aside"`
}

// NewWindow is a Window in the spec's date form.
func NewWindow(from, to time.Time, setAside int) *Window {
	return &Window{From: from.Format(time.DateOnly), To: to.Format(time.DateOnly), SetAside: setAside}
}

// Settle fills Status from the rest of the block. Anything that went wrong
// makes it partial rather than failed, because a block that is being settled
// had a result to report — failed is only for an answer that never got one,
// and Failure builds that. Held back for budget is partial too: the reader was
// told less than matched and has to know to ask with more room.
func (b *Block) Settle() {
	if b.Searched == nil {
		b.Searched = []Searched{}
	}
	if b.Errors == nil {
		b.Errors = []string{}
	}
	switch {
	case len(b.Errors) > 0 || b.Omitted.Budget > 0:
		b.Status = StatusPartial
	case b.Returned == 0:
		b.Status = StatusEmpty
	default:
		b.Status = StatusOK
	}
}

// Absorb moves an Outcome's notes into the block: a failed note is an error, the
// rest are notes.
func (b *Block) Absorb(o Outcome) {
	for _, n := range o.Notes {
		if n.Failed {
			b.Errors = append(b.Errors, n.Text)
		} else {
			b.Notes = append(b.Notes, n.Text)
		}
	}
}

// Failure is the block for a command that could not answer at all.
func Failure(err error) Block {
	return Block{Status: StatusFailed, Searched: []Searched{}, Errors: []string{err.Error()}}
}
