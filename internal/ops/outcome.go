// Package ops is each operation once, for every surface. The CLI, the MCP
// server and the hooks used to each call the memory package and compose their
// own report of what happened, and the reports drifted: #241 was the review
// queue's hand-edit announcement fixed for one surface while another said
// nothing, and a remember whose vault write failed lost its memory id on both.
// An operation here returns what it found plus an Outcome — what it did on the
// way, with counts, and what failed — and a surface only renders it.
package ops

import (
	"errors"
	"strings"
)

// Note is one thing an operation has to say beyond its result. Ordered, not
// keyed, because the order is part of the report: the adoption of a hand edit
// reads before the count it changed.
type Note struct {
	Text   string `json:"text"`
	Failed bool   `json:"failed,omitempty"`
}

// Outcome is what an operation did besides answering, and what went wrong.
// A failure that does not void the result — the review queue could not be
// counted, the vault refused a write the cache took — is a failed note rather
// than an error, so the half that succeeded is still reported (invariant 4).
type Outcome struct {
	Notes []Note `json:"notes,omitempty"`
}

func (o *Outcome) say(text string) {
	if text != "" {
		o.Notes = append(o.Notes, Note{Text: text})
	}
}

func (o *Outcome) fail(text string) {
	o.Notes = append(o.Notes, Note{Text: text, Failed: true})
}

// Failed reports whether any part of the operation failed.
func (o Outcome) Failed() bool {
	for _, n := range o.Notes {
		if n.Failed {
			return true
		}
	}
	return false
}

// Err is the failed notes as one error, or nil, for a surface whose exit
// status has to carry the failure.
func (o Outcome) Err() error {
	var failed []string
	for _, n := range o.Notes {
		if n.Failed {
			failed = append(failed, n.Text)
		}
	}
	if len(failed) == 0 {
		return nil
	}
	return errors.New(strings.Join(failed, "; "))
}

// Text renders the notes as paragraphs to follow a result, a failure in
// parentheses so it reads as a remark on the result rather than a replacement
// for it.
func (o Outcome) Text() string {
	return o.render(true)
}

// TextWithoutFailures is Text less the failed notes, for a surface that
// reports those separately through Err.
func (o Outcome) TextWithoutFailures() string {
	return o.render(false)
}

func (o Outcome) render(failures bool) string {
	var b strings.Builder
	for _, n := range o.Notes {
		switch {
		case !n.Failed:
			b.WriteString("\n\n" + n.Text)
		case failures:
			b.WriteString("\n\n(" + n.Text + ")")
		}
	}
	return b.String()
}
