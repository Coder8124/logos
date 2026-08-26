// Package eval is a benchmark for agentic memory and handoff.
//
// # Why another benchmark
//
// LongMemEval and its relatives measure one thing well: given a question, can
// the store surface the session holding the answer. That is recall, and recall
// is the easy half. It says nothing about the question this project exists to
// answer — an agent stopped mid-task, a *different* agent arrives, and the user
// re-explains nothing. Nobody scores that, so nobody optimises for it.
//
// So this suite measures continuity: what survives the boundary between two
// agents. The metric that matters most is the one no recall benchmark has a
// slot for — whether the approaches already ruled out make it across. Anyone
// can restate a goal. The expensive knowledge is the three things that didn't
// work, and it is exactly what dies when a session ends.
//
// # The honest half
//
// A benchmark whose author picks the categories is a benchmark its author wins.
// The counterweight is deliberate: this suite includes families brain is known
// to be bad at, several of them found by an agent criticising brain's own
// output during a live handoff — stale context presented without its age,
// circular sourcing where prose restates a claim the data contradicts, and
// abstention, which the LongMemEval harness in memory/bench.go explicitly
// filters out before scoring. Those cases are marked Expect: Fail below. If a
// change makes them pass, that is progress; if a change makes a passing case
// fail, the suite says so.
//
// # What is compared
//
// Every system under test implements Adapter — write events, read back context
// for a task. That common denominator is the point. brain has checkpoint and
// resume as primitives; a store with only add() and search() must flatten a
// checkpoint into prose. The asymmetry is not a handicap imposed by the
// harness, it is the finding.
package eval

import "strings"

// Kind classifies an event, because systems with richer primitives are allowed
// to use them. An adapter that has no notion of a checkpoint is expected to
// flatten one into ordinary text — see Event.Flatten.
type Kind string

const (
	// KindMessage is a conversational turn.
	KindMessage Kind = "message"
	// KindFact is a durable statement about the user, the sort a memory tool
	// stores verbatim.
	KindFact Kind = "fact"
	// KindDoc is a document that exists in the world — a spec, a spreadsheet
	// export, a project page.
	KindDoc Kind = "doc"
	// KindNote is a line of working progress, written while the work happens.
	KindNote Kind = "note"
	// KindCheckpoint is where an agent stopped: state, decisions, what failed,
	// what is next.
	KindCheckpoint Kind = "checkpoint"
)

// An Event is one thing that happened, in the order it happened. Scenarios are
// built from these, and every adapter receives exactly the same sequence.
type Event struct {
	TS      int64  // unix seconds; scenarios use real offsets so staleness is testable
	Actor   string // "user", "claude", "cursor" — who produced this
	Kind    Kind
	Project string
	Title   string // for KindDoc
	Text    string

	// Checkpoint fields. Adapters with a checkpoint primitive should map these
	// onto it; the rest get them via Flatten.
	Task      string
	Decisions []string
	Failed    []string // the approaches already ruled out
	Questions []string
	Next      string
}

// Flatten renders an event as the plain prose a store without structured
// primitives would have to swallow. It is deliberately generous — everything is
// present, nothing is abbreviated — so that a system scoring badly on this
// suite cannot blame the harness for withholding information.
func (e Event) Flatten() string {
	if e.Kind != KindCheckpoint {
		if e.Title != "" {
			return e.Title + "\n" + e.Text
		}
		return e.Text
	}

	var b strings.Builder
	b.WriteString("Checkpoint by " + e.Actor)
	if e.Project != "" {
		b.WriteString(" on " + e.Project)
	}
	b.WriteString(".\n")
	section := func(label, body string) {
		if strings.TrimSpace(body) != "" {
			b.WriteString(label + ": " + body + "\n")
		}
	}
	list := func(label string, items []string) {
		if len(items) > 0 {
			b.WriteString(label + ": " + strings.Join(items, "; ") + "\n")
		}
	}
	section("Task", e.Task)
	section("State", e.Text)
	list("Decisions", e.Decisions)
	list("Already tried and it did not work", e.Failed)
	list("Open questions", e.Questions)
	section("Next", e.Next)
	return b.String()
}

// A Query is what the arriving agent asks for. Budget is a token ceiling: a
// system may return less, but returning more is measured and counted against
// it, because context that overflows the window is context nobody reads.
type Query struct {
	Task    string
	Project string
	Agent   string // who is asking now — for handoff cases, not whoever wrote the events
	Budget  int
	Now     int64 // the clock at read time, so staleness has a reference point
}

// A Response is what the system would put into the arriving agent's context
// window. Text is scored; Tokens is measured by the harness, not by the
// adapter, so nobody can under-report their own cost.
type Response struct {
	Text string
	// Err is set when the system declined or failed to answer. A failed read is
	// scored as an empty response rather than aborting the run — a memory layer
	// that errors on an unfamiliar question is one an agent learns to avoid, and
	// that should show up in the numbers rather than in a stack trace.
	Err error
}

// An Adapter is a memory system under test. Reset must leave it as empty as a
// fresh install: the suite runs every scenario against a clean store, so a
// system cannot score by accumulating context across cases.
type Adapter interface {
	Name() string
	Reset() error
	Write(Event) error
	Read(Query) (Response, error)
	Close() error
}

// Durable is implemented by systems that distinguish a source of truth from a
// rebuildable cache. DropDerived deletes everything the system considers
// disposable; whatever survives is what the system really owns.
//
// Not implementing this is itself an answer, and the suite reports it as one:
// a store whose only copy lives in its own index has no source of truth, and
// scores zero on the durability family rather than being excused from it.
type Durable interface {
	DropDerived() error
}

// Tokens estimates cost at four characters per token.
//
// A real tokenizer would be a model-specific dependency answering a question
// that only needs to be roughly right, and — more to the point — every adapter
// is measured with this same function. Consistency across systems matters more
// here than absolute accuracy against any one vocabulary.
func Tokens(s string) int {
	if s == "" {
		return 0
	}
	return len(s)/4 + 1
}
