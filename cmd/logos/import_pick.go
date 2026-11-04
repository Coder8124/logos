package main

import (
	"fmt"
	"os"
	"strings"
)

// importSource is one place `logos import` reads from, as its menu offers it.
// Each runs exactly what `logos import --from <from>` runs, so a source picked
// from the menu previews and asks the same way it does when typed.
type importSource struct {
	from, label string
	run         func(args []string) error
}

// importSources is in the order help lists them. The label says where each
// one lands, because that differs: claude-mem becomes memories, the rules
// files wait for review, transcripts become checkpoint candidates. A func, not
// a var: the claude-mem row calls runImport, which opens this menu.
func importSources() []importSource {
	return []importSource{
		{claudeMemSource, "claude-mem's observations — become memories", func(args []string) error {
			return runImport(append([]string{"--from", claudeMemSource}, args...))
		}},
		{claudeMDSource, "CLAUDE.md and AGENTS.md — queued for review", func(args []string) error {
			return runImportRules(claudeMDSource, args)
		}},
		{cursorRulesSource, "Cursor rules — queued for review", func(args []string) error {
			return runImportRules(cursorRulesSource, args)
		}},
		{claudeAutoMemorySource, "Claude Code's auto memory — queued for review", func(args []string) error {
			return runImportRules(claudeAutoMemorySource, args)
		}},
		{"transcripts", "agent transcripts — become checkpoint candidates", runIngest},
		{"git", "this repository's git history — seeds a cold vault", runBootstrap},
	}
}

// chooseImportSources asks which sources to import from. Enter with nothing
// ticked takes the highlighted row, so one source is still one keypress;
// ok=false is a cancel.
func chooseImportSources() ([]importSource, bool, error) {
	sources := importSources()
	rows := make([]string, len(sources))
	for i, s := range sources {
		rows[i] = s.label
	}
	checked := make([]bool, len(sources))
	i, res, _, err := pickOnTerminal(menu{title: "Import from which sources?", hint: "Space to tick, Enter to import, q to cancel", rows: rows, checked: checked})
	if err != nil {
		return nil, false, err
	}
	// Nothing comes before this menu, so Esc is a cancel too.
	if res != picked {
		return nil, false, nil
	}
	var chosen []importSource
	for j, c := range checked {
		if c {
			chosen = append(chosen, sources[j])
		}
	}
	if len(chosen) == 0 {
		chosen = []importSource{sources[i]}
	}
	return chosen, true, nil
}

// runImportPicked is a bare `logos import` at a terminal. The flags given
// (--dry-run, --yes, --project) go to every source chosen. One source failing
// does not stop the rest — they share nothing — but each failure is named as
// it happens and again in the error the command exits with, so a run that
// imported two of three never reads as having imported all three.
func runImportPicked(args []string) error {
	chosen, ok, err := chooseImportSources()
	if err != nil {
		return fmt.Errorf("could not show the import menu (%v); name a source with --from", err)
	}
	if !ok {
		fmt.Println("Nothing imported — the menu was cancelled.")
		return nil
	}
	var failed []string
	for i, s := range chosen {
		if len(chosen) > 1 {
			if i > 0 {
				fmt.Println()
			}
			fmt.Printf("── %d of %d: logos import --from %s\n\n", i+1, len(chosen), s.from)
		}
		if err := s.run(args); err != nil {
			fmt.Fprintf(os.Stderr, "logos: import --from %s: %v\n", s.from, err)
			failed = append(failed, s.from)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("%d of %d sources failed: %s", len(failed), len(chosen), strings.Join(failed, ", "))
	}
	return nil
}
