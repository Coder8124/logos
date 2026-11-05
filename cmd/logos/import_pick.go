package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// importSource is one place `logos import` reads from, as its menu offers it.
// Each runs exactly what `logos import --from <from>` runs, so a source picked
// from the menu previews and asks the same way it does when typed.
//
// dir, when set, is the folder the source reads instead of this one: the
// menu's folder row runs the rules sources with --dir.
type importSource struct {
	from, label, dir string
	run              func(args []string) error
}

// importSources is in the order help lists them. The label says where each
// one lands, because that differs: claude-mem becomes memories, the rules
// files wait for review, transcripts become checkpoint candidates. A func, not
// a var: the claude-mem row calls runImport, which opens this menu.
func importSources() []importSource {
	return []importSource{
		{from: claudeMemSource, label: "claude-mem's observations — become memories", run: func(args []string) error {
			return runImport(append([]string{"--from", claudeMemSource}, args...))
		}},
		{from: claudeMDSource, label: "CLAUDE.md and AGENTS.md — queued for review", run: func(args []string) error {
			return runImportRules(claudeMDSource, args)
		}},
		{from: cursorRulesSource, label: "Cursor rules — queued for review", run: func(args []string) error {
			return runImportRules(cursorRulesSource, args)
		}},
		{from: claudeAutoMemorySource, label: "Claude Code's auto memory — queued for review", run: func(args []string) error {
			return runImportRules(claudeAutoMemorySource, args)
		}},
		{from: "transcripts", label: "agent transcripts — become checkpoint candidates", run: runIngest},
		{from: "git", label: "this repository's git history — seeds a cold vault", run: runBootstrap},
	}
}

// folderSources are what a folder named in the menu is read for: the sources
// that take --dir and read files a person keeps in a folder. git is left out:
// it reads a repository's history, not what is in the folder.
var folderSources = []string{claudeMDSource, cursorRulesSource, claudeAutoMemorySource}

// chooseImportSources asks which sources to import from. Enter or a click
// ticks a row and the last row starts the import, so several sources are as
// easy as one; the folder row is typed into, for rules kept somewhere other
// than here. ok=false is a cancel.
func chooseImportSources() ([]importSource, bool, error) {
	sources := importSources()
	rows := make([]string, 0, len(sources)+1)
	for _, s := range sources {
		rows = append(rows, s.label)
	}
	rows = append(rows, "a folder you name — its CLAUDE.md, AGENTS.md, Cursor rules and Claude Code memory")
	checked := make([]bool, len(rows))
	folder := &menuField{row: len(sources), prompt: "Folder: ", check: importFolder}
	_, res, _, err := pickOnTerminal(menu{
		title: "Import from which sources?", hint: "Enter or click ticks, then Import; q cancels",
		rows: rows, checked: checked, submit: "Import the ticked sources", field: folder, mouse: true,
	})
	if err != nil {
		return nil, false, err
	}
	// Nothing comes before this menu, so Esc is a cancel too.
	if res != picked {
		return nil, false, nil
	}
	var chosen []importSource
	for j, s := range sources {
		if checked[j] {
			chosen = append(chosen, s)
		}
	}
	if checked[folder.row] && folder.value != "" {
		for _, from := range folderSources {
			for _, s := range sources {
				if s.from == from {
					s.dir = folder.value
					chosen = append(chosen, s)
				}
			}
		}
	}
	return chosen, true, nil
}

// importFolder is the folder row's check: what was typed, as a folder that
// exists. A folder dragged into a terminal arrives quoted or with its spaces
// escaped, and ~ is not expanded by anything but a shell, so all three are
// undone here rather than refused.
func importFolder(typed string) (string, error) {
	p := strings.TrimSpace(typed)
	if len(p) >= 2 && (p[0] == '\'' || p[0] == '"') && p[len(p)-1] == p[0] {
		p = p[1 : len(p)-1]
	} else {
		p = strings.ReplaceAll(p, "\\ ", " ")
	}
	if p == "" {
		return "", nil
	}
	p, err := filepath.Abs(expandHome(p))
	if err != nil {
		return "", err
	}
	info, err := os.Stat(p)
	// The reasons leave the path out: the field above shows it, and a path
	// repeated here would cut the reason off an 80-column line.
	switch {
	case os.IsNotExist(err):
		return "", fmt.Errorf("no folder by that name")
	case err != nil:
		return "", err
	case !info.IsDir():
		return "", fmt.Errorf("that is a file, not a folder")
	}
	return p, nil
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
		run, name := args, s.from
		if s.dir != "" {
			// The menu's folder replaces a --dir typed with the command:
			// it was chosen later, and for this source.
			run = append(dropFlag(args, "--dir"), "--dir", s.dir)
			name += " --dir " + s.dir
		}
		// Headed even when alone: one folder source is not the only one the
		// folder row runs, and an import from somewhere else must say where.
		if len(chosen) > 1 || s.dir != "" {
			if i > 0 {
				fmt.Println()
			}
			fmt.Printf("── %d of %d: logos import --from %s\n\n", i+1, len(chosen), name)
		}
		if err := s.run(run); err != nil {
			fmt.Fprintf(os.Stderr, "logos: import --from %s: %v\n", name, err)
			failed = append(failed, name)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("%d of %d sources failed: %s", len(failed), len(chosen), strings.Join(failed, ", "))
	}
	return nil
}
