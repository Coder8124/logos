package main

import (
	"errors"
	"fmt"
	"strings"
)

// homeRow is one command a bare `logos` offers. Only commands that do
// something useful with nothing typed after them are here: a command that
// needs a project name or a path would open, fail on its usage line, and
// teach that the menu is a trap. search is the exception, because its row
// is a text box that takes the query.
type homeRow struct {
	cmd, label string
}

// homeRows are in the order help lists them, with resume first and
// preselected: picking up where the last agent stopped is what most people
// open logos for, so Enter alone does it.
var homeRows = []homeRow{
	{"resume", "resume   pick up where the last agent left off"},
	{"search", "search   search the vault — type the query"},
	{"show", "show     one project on one screen"},
	{"stats", "stats    patterns, usage, what memory leans on"},
	{"memory", "memory   what logos knows"},
	{"review", "review   accept or reject what is waiting for your yes"},
	{"import", "import   bring in CLAUDE.md, Cursor rules, claude-mem, git"},
	{"watch", "watch    follow what agents record as it happens"},
	{"config", "config   settings"},
	{"index", "index    rebuild the cache from the vault"},
	{"doctor", "doctor   check the vault, the index and the hosts"},
	{"setup", "setup    connect logos to the AI agents on this machine"},
	{"update", "update   update logos to the latest release"},
	{"help", "help     every command, with what it takes"},
}

// runHomeMenu is a bare `logos` at a terminal. The picked command goes
// through runTyped, so a command with forms opens its own menu next, as it
// would typed bare. The command line it stands for is printed first: the
// menu should teach the commands, not replace them.
func runHomeMenu(runOne func(string, []string) error) error {
	rows := make([]string, len(homeRows))
	searchRow := 0
	for i, r := range homeRows {
		rows[i] = r.label
		if r.cmd == "search" {
			searchRow = i
		}
	}
	query := &menuField{row: searchRow, prompt: "search for: ", check: func(s string) (string, error) {
		s = strings.TrimSpace(s)
		if s == "" {
			return "", errors.New("type something to search for")
		}
		return s, nil
	}}
	i, res, _, err := pickOnTerminal(menu{
		title: "logos — what now?", hint: "Enter or click runs it, q to cancel",
		rows: rows, field: query, mouse: true,
	})
	if err != nil {
		return fmt.Errorf("could not show the menu (%v); `logos help` lists the commands", err)
	}
	if res != picked {
		fmt.Println("Nothing run — the logos menu was cancelled.")
		return nil
	}
	cmd := homeRows[i].cmd
	if cmd == "search" {
		// Split the way a shell would split it unquoted, so a typed
		// --answer is the flag it is on the command line.
		args := strings.Fields(query.value)
		fmt.Printf("→ logos search %s\n", query.value)
		return runTyped(cmd, args, runOne)
	}
	fmt.Printf("→ logos %s\n", cmd)
	return runTyped(cmd, nil, runOne)
}
