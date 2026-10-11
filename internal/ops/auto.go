package ops

import (
	"fmt"
	"strings"
	"time"

	"github.com/Coder8124/logos/internal/auto"
	"github.com/Coder8124/logos/internal/session"
)

// autoLookback bounds the report for a project that has never had a checkpoint
// of its own, so a first resume does not recite a year of the log.
const autoLookback = 7 * 24 * time.Hour

// AutoReport is what resume says about auto: what it did out of sight since
// the project's last checkpoint an agent wrote, and anything wrong with
// auto.md. Since that checkpoint and not since the last one of any kind, because
// the latest is often the one auto wrote at session end, and the report is
// there to say it was auto that wrote it.
func AutoReport(vault, project string) Outcome {
	var o Outcome
	since := time.Now().Add(-autoLookback)
	history, err := session.History(vault, project, 0)
	if err != nil {
		o.fail(fmt.Sprintf("could not read %s's checkpoints to say what auto did: %v", project, err))
		return o
	}
	for _, c := range history {
		if !c.Auto {
			since = time.Unix(c.TS, 0)
			break
		}
	}
	entries, err := auto.Entries(vault, project, earlier(since, time.Now().Add(-2*auto.DreamEvery)))
	if err != nil {
		o.fail(fmt.Sprintf("could not read %s: %v", auto.LogFile, err))
	} else {
		o.say(auto.Summary(lastDreamAnd(entries, since)))
	}
	c, err := auto.Load(vault)
	if err != nil {
		o.fail(fmt.Sprintf("could not read %s, so nothing runs on its own until it can: %v", auto.File, err))
	}
	for _, p := range c.Problems {
		o.say(fmt.Sprintf("%s: %s", auto.File, p))
	}
	return o
}

func earlier(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// lastDreamAnd keeps the entries since the checkpoint, and of dream's only the
// latest run, whenever it was. A dream finishes minutes after the resume that
// launched it and before the agent's own checkpoint, so "since the checkpoint"
// would never show one; and only the latest, so resume reports last night and
// not the week.
func lastDreamAnd(entries []auto.Entry, since time.Time) []auto.Entry {
	from := -1
	for i, e := range entries {
		if e.Switch == auto.Dream && (e.Started || from < 0) {
			from = i
		}
	}
	var out []auto.Entry
	for i, e := range entries {
		if e.Switch == auto.Dream {
			if i >= from {
				out = append(out, e)
			}
		} else if !e.At.Before(since) {
			out = append(out, e)
		}
	}
	return out
}

// AutoLead is what resume does about auto, as the lines that open it, the same
// on the CLI and over MCP, or "" when there is nothing to say: AutoReport, then
// the day's dream if it is due. The report comes first so it covers the last
// dream, not the one just launched.
func AutoLead(vault, project string) string {
	o := AutoReport(vault, project)
	switch started, err := auto.StartDream(vault, time.Now()); {
	case err != nil:
		o.fail(fmt.Sprintf("auto: the day's dream was not started: %v", err))
	case started:
		o.say("auto: dream started in the background; the next resume says what it did")
	}
	text := strings.TrimLeft(o.Text(), "\n")
	if text == "" {
		return ""
	}
	return text + "\n\n"
}
