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
	entries, err := auto.Entries(vault, project, since)
	if err != nil {
		o.fail(fmt.Sprintf("could not read %s: %v", auto.LogFile, err))
	} else {
		o.say(auto.Summary(entries))
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

// AutoLead is AutoReport as the lines that open a resume, the same on the CLI
// and over MCP, or "" when there is nothing to say.
func AutoLead(vault, project string) string {
	text := strings.TrimLeft(AutoReport(vault, project).Text(), "\n")
	if text == "" {
		return ""
	}
	return text + "\n\n"
}
