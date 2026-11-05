package main

import (
	"fmt"
	"os"
	"strings"
)

// flagRow is one line of a command's flag menu. A mode row is a form of the
// command on its own (`show --history`); a modifier row adjusts one mode
// (`--list` belongs to `--graph`), so ticking it alone still means its mode.
// The bare command is the mode keyed "".
type flagRow struct {
	flags string // as typed, space-separated; "" for the bare command
	label string
	mode  string // the mode this row runs under: its own flags for a mode row
}

func (r flagRow) isMode() bool { return r.mode == r.flags }

// mode and modifier keep the table below readable: a modifier names the mode
// it belongs to.
func mode(flags, label string) flagRow { return flagRow{flags, label, flags} }
func modifier(of, flags, label string) flagRow {
	return flagRow{flags, label, of}
}

// flagMenus are the commands that do one thing bare and others behind flags.
// The first row is the bare command and is preselected, so Enter alone does
// what the command did before it had a menu. Each flag is shown as it is
// typed, because the menu is also how a person learns the flag. A flag that
// takes a value (--project P, --since 24h) is left out: the menu cannot type
// it, and the command line still can.
var flagMenus = map[string][]flagRow{
	"show": {
		mode("", "this project on one screen: checkpoint, ruled-out approaches, memories"),
		modifier("", "--all", "with every item, not the first few"),
		mode("--history", "checkpoint history, and any abandoned ones"),
		mode("--plans", "plan-mode plans saved when ExitPlanMode was approved"),
		mode("--continuity", "vault-wide: which projects checkpoint, which have gone quiet"),
		mode("--projects", "auto-detected projects"),
		mode("--graph", "draw this project with its checkpoints and memories"),
		modifier("--graph", "--similar", "with the similar-notes edges"),
		modifier("--graph", "--list", "printed as text instead of drawn"),
		mode("--since-last", "catch up on what changed since you were last here"),
		modifier("--since-last", "--peek", "without marking it seen"),
	},
	"stats": {
		mode("", "patterns in the vault: a recurring blocker, a dormant memory"),
		mode("--usage", "what context packs left out, dead ends handed back, handoffs"),
		modifier("--usage", "--week", "the last 7 days only"),
		mode("--memory", "composition, growth, and what your memory leans on"),
	},
	"review": {
		mode("", "accept or reject quarantined memories, the first 20"),
		modifier("", "--all", "every pending memory"),
		mode("--candidates", "transcript candidates, before they become checkpoints"),
		mode("--connections", "the connections a dream proposed overnight"),
		mode("--stale", "rulings and memories whose files changed since they were recorded"),
		mode("--auto", "what the review switch accepted for you"),
	},
	"doctor": {
		mode("", "health of the vault, index and hosts"),
		modifier("", "--verbose", "add runtimes and tiers"),
		modifier("", "--probe", "verify each model actually loads"),
		mode("--integration", "prove a host can reach logos"),
		mode("--report", "a paste-able bundle for a bug report"),
		mode("--egress", "every address logos can contact"),
	},
	"watch": {
		mode("", "captures, rulings and memory changes as they happen"),
		mode("--log", "every prompt, tool call and turn the host reported"),
		mode("--log --projects", "which projects are being recorded"),
		mode("--log --compliance", "how often a checkpoint ask was followed by one"),
	},
}

// flagMenuRow is how a row is drawn: the flag first, as typed, then what it
// does. Modifiers are indented under their mode so the table reads as a tree.
func flagMenuRow(cmd string, r flagRow, width int) string {
	switch {
	case r.flags == "":
		return fmt.Sprintf("%-*s  %s", width, "logos "+cmd, r.label)
	case r.isMode():
		return fmt.Sprintf("%-*s  %s", width, r.flags, r.label)
	default:
		return fmt.Sprintf("%-*s  %s", width, "  + "+r.flags, r.label)
	}
}

// flagRuns turns the rows ticked (or, with none ticked, the one highlighted)
// into the command lines to run, one per mode, in menu order. A modifier
// ticked without its mode runs under it: ticking --list alone is a request
// for the graph as text, and reading it as nothing would throw it away.
func flagRuns(rows []flagRow, checked []bool, sel int) [][]string {
	chosen := map[int]bool{}
	for i, c := range checked {
		if c {
			chosen[i] = true
		}
	}
	if len(chosen) == 0 {
		chosen[sel] = true
	}
	extra := map[string][]string{}
	for i, r := range rows {
		if !chosen[i] {
			continue
		}
		if _, seen := extra[r.mode]; !seen {
			extra[r.mode] = nil
		}
		if !r.isMode() {
			extra[r.mode] = append(extra[r.mode], strings.Fields(r.flags)...)
		}
	}
	// Modes in menu order, not tick order, so the same ticks always run the
	// same way.
	var runs [][]string
	for _, r := range rows {
		if !r.isMode() {
			continue
		}
		mods, ok := extra[r.mode]
		if !ok {
			continue
		}
		runs = append(runs, append(strings.Fields(r.flags), mods...))
	}
	return runs
}

// runFlagMenu is a bare command at a terminal: a checklist of its flags, then
// each chosen form run through runOne as if typed. The command line is
// printed before it runs, so the menu teaches the flag it saved typing. One
// form failing does not stop the rest, and the error it exits with names
// every one that failed, so two of three never reads as all three.
func runFlagMenu(cmd string, rows []flagRow, runOne func(string, []string) error) error {
	width := 0
	for _, r := range rows {
		w := len(r.flags)
		if r.flags == "" {
			w = len("logos " + cmd)
		} else if !r.isMode() {
			w += 4
		}
		width = max(width, w)
	}
	lines := make([]string, len(rows))
	for i, r := range rows {
		lines[i] = flagMenuRow(cmd, r, width)
	}
	checked := make([]bool, len(rows))
	sel, res, _, err := pickOnTerminal(menu{
		title: "logos " + cmd + " — which form?",
		hint:  "Space to tick, Enter to run, q to cancel",
		rows:  lines, checked: checked,
	})
	if err != nil {
		return fmt.Errorf("could not show the menu (%v); type the flags instead — `logos help %s`", err, cmd)
	}
	if res != picked {
		fmt.Printf("Nothing run — the logos %s menu was cancelled.\n", cmd)
		return nil
	}
	runs := flagRuns(rows, checked, sel)
	var failed []string
	for i, args := range runs {
		line := strings.TrimSpace("logos " + cmd + " " + strings.Join(args, " "))
		if len(runs) > 1 {
			if i > 0 {
				fmt.Println()
			}
			fmt.Printf("── %d of %d: %s\n\n", i+1, len(runs), line)
		} else {
			fmt.Printf("── %s\n\n", line)
		}
		if err := runOne(cmd, args); err != nil {
			if len(runs) == 1 {
				return err
			}
			fmt.Fprintf(os.Stderr, "logos: %s: %v\n", strings.TrimPrefix(line, "logos "), err)
			failed = append(failed, line)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("%d of %d failed: %s", len(failed), len(runs), strings.Join(failed, "; "))
	}
	return nil
}

// configChoice is one value a setting can be given from the config menu.
type configChoice struct {
	args  []string // after `logos config <setting>`
	label string
}

// configMenu is every setting the menu offers, in help's order, with the
// values it can be set to. Where the bare setting only reports, that report
// is first and preselected, so Enter, Enter changes nothing. deny is the
// exception: bare, it acts — it writes the marker that keeps Logos out — so
// both rows say what they do and neither is a look. key is left out: it needs
// a keychain ref typed after it.
var configMenu = []struct {
	setting, label string
	choices        []configChoice
}{
	{"announce", "how loudly Logos reports its own work", []configChoice{
		{nil, "show the current setting"},
		{[]string{"on"}, "on — a marked receipt when Logos stores, recalls or checkpoints"},
		{[]string{"quiet"}, "quiet — the same facts, without the marker"},
		{[]string{"off"}, "off — nothing extra; the agent is still told, you are not"},
	}},
	{"auto", "what Logos does on its own, as one switch", []configChoice{
		{nil, "show the current setting"},
		{[]string{"on"}, "on"},
		{[]string{"off"}, "off"},
		{[]string{"config"}, "list the switches it covers, to choose among them"},
	}},
	{"think", "how much the model reasons before answering", []configChoice{
		{nil, "show the current setting"},
		{[]string{"off"}, "off"},
		{[]string{"low"}, "low"},
		{[]string{"medium"}, "medium"},
		{[]string{"high"}, "high"},
	}},
	{"activity", "recording the activity log, for this vault", []configChoice{
		{nil, "show the current setting"},
		{[]string{"on"}, "on — record every prompt and tool call the host reports"},
		{[]string{"off"}, "off — stop recording"},
	}},
	{"deny", "keep Logos out of this repository", []configChoice{
		{nil, "keep Logos out of this repository (writes .logos/deny)"},
		{[]string{"--undo"}, "let Logos back in (removes .logos/deny)"},
	}},
	{"prompt", "the instructions agents are given (LOGOSPROMPT.md)", nil},
}

// runConfigMenu is a bare `logos config` at a terminal: which setting, then
// which value. Esc in the values goes back to the settings, so a wrong pick
// costs a keypress. The first row lists the settings, which is what a bare
// config did before.
func runConfigMenu(runOne func(string, []string) error) error {
	rows := []string{"list the settings"}
	width := 0
	for _, s := range configMenu {
		width = max(width, len(s.setting))
	}
	for _, s := range configMenu {
		rows = append(rows, fmt.Sprintf("%-*s  %s", width, s.setting, s.label))
	}
	settings := menu{title: "logos config — which setting?", hint: "Enter to choose, q to cancel", rows: rows}
	for {
		i, res, lines, err := pickOnTerminal(settings)
		switch {
		case err != nil:
			return fmt.Errorf("could not show the menu (%v); type the setting instead — `logos help config`", err)
		case res != picked:
			fmt.Println("Nothing changed — the logos config menu was cancelled.")
			return nil
		case i == 0:
			return runOne("config", nil)
		}
		s := configMenu[i-1]
		var args []string
		if len(s.choices) > 0 {
			vals := make([]string, len(s.choices))
			for j, c := range s.choices {
				vals[j] = c.label
			}
			j, res, valLines, err := pickOnTerminal(menu{title: "logos config " + s.setting, hint: "Enter to choose, Esc to go back, q to cancel", rows: vals})
			switch {
			case err != nil:
				return fmt.Errorf("could not show the menu (%v); type the setting instead — `logos help config`", err)
			case res == cancelled:
				fmt.Println("Nothing changed — the logos config menu was cancelled.")
				return nil
			case res == wentBack:
				settings.sel, settings.over = i, lines+valLines
				continue
			}
			args = s.choices[j].args
		}
		fmt.Printf("── %s\n\n", strings.TrimSpace("logos config "+s.setting+" "+strings.Join(args, " ")))
		return runOne("config", append([]string{s.setting}, args...))
	}
}
