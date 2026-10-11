package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Coder8124/logos/internal/auto"
)

const autoUsage = "usage: logos auto [on|off|status|config [<switch> on|off]]"

// runAuto is `logos auto`: one switch for everything Logos does on its own,
// and `config` for choosing which of it.
func runAuto(args []string) error {
	vault, err := requireVault()
	if err != nil {
		return err
	}
	verb := ""
	if len(args) > 0 {
		verb = strings.ToLower(args[0])
	}
	switch {
	case verb == "" || verb == "on":
		if len(args) > 1 {
			return fmt.Errorf(autoUsage)
		}
		if err := auto.SetMaster(vault, true); err != nil {
			return err
		}
	case verb == "off":
		if len(args) > 1 {
			return fmt.Errorf(autoUsage)
		}
		if err := auto.SetMaster(vault, false); err != nil {
			return err
		}
	case verb == "status" && len(args) == 1:
		if err := printAutoSwitches(os.Stdout, vault); err != nil {
			return err
		}
		return printAutoLog(os.Stdout, vault)
	case verb == "config" && len(args) == 1:
		printAutoSwitches(os.Stdout, vault)
		printAutoFixed(os.Stdout)
		fmt.Println("\n  logos auto config <switch> on|off")
		return nil
	case verb == "config" && len(args) == 3:
		on, ok := map[string]bool{"on": true, "off": false}[strings.ToLower(args[2])]
		if !ok {
			return fmt.Errorf(autoUsage)
		}
		if err := auto.Set(vault, args[1], on); err != nil {
			return err
		}
	default:
		return fmt.Errorf(autoUsage)
	}
	// Every form ends with the state it left, including a change: a command
	// whose job is to change what runs unattended has to say what now does.
	return printAutoSwitches(os.Stdout, vault)
}

// printAutoSwitches prints each switch, whether it is on, and what decided
// that. It is also what `setup --print-config` shows, so it takes a writer.
func printAutoSwitches(w io.Writer, vault string) error {
	c, err := auto.Load(vault)
	if err != nil {
		fmt.Fprintf(w, "auto: %s could not be read (%v) — nothing runs on its own until it can\n", auto.Path(vault), err)
		return err
	}
	switch {
	case c.Master == nil:
		fmt.Fprintln(w, "auto: not set — Logos does what it did before the switch; `logos auto on` or `off` decides")
	case *c.Master:
		fmt.Fprintln(w, "auto: on")
	default:
		fmt.Fprintln(w, "auto: off")
	}
	for _, s := range auto.Switches() {
		state := "off"
		if c.On(s) {
			state = "on"
		}
		fmt.Fprintf(w, "  %-16s %-4s %-12s %s\n", s, state, c.Why(s), auto.Describe(s))
	}
	for _, p := range c.Problems {
		fmt.Fprintf(w, "  ! %s: %s\n", auto.File, p)
	}
	return nil
}

func printAutoFixed(w io.Writer) {
	fmt.Fprintln(w, "\nnever automatic, whatever auto.md says:")
	for _, f := range auto.Fixed {
		fmt.Fprintf(w, "  %-16s %s\n", f.Name, f.Why)
	}
}

// logAuto records what auto did out of sight. A log that cannot be written is
// said on stderr: the action itself happened and was reported where it ran,
// but the next resume will not repeat it, and that is worth knowing.
func logAuto(e auto.Entry) {
	if err := auto.Record(vaultPath(), e); err != nil {
		fmt.Fprintf(os.Stderr, "logos: could not write %s, so the next resume will not mention this: %v\n", auto.LogFile, err)
	}
}

// printAutoLog is the week of what auto did, every project, one line each, so
// "what has Logos been doing without me" has an answer that is not a resume.
func printAutoLog(w io.Writer, vault string) error {
	entries, err := auto.Entries(vault, "", time.Now().Add(-7*24*time.Hour))
	if err != nil {
		return fmt.Errorf("could not read %s: %w", auto.LogPath(vault), err)
	}
	if len(entries) == 0 {
		fmt.Fprintf(w, "\nauto has done nothing out of sight in the last week (%s)\n", auto.LogPath(vault))
		return nil
	}
	fmt.Fprintf(w, "\nthe last week, from %s:\n", auto.LogPath(vault))
	for _, e := range entries {
		what := fmt.Sprintf("%s n=%d", e.Switch, e.N)
		if e.Failed != "" {
			what = fmt.Sprintf("%s failed: %s", e.Switch, e.Failed)
		}
		where := ""
		if e.Project != "" {
			where = " on " + e.Project
		}
		fmt.Fprintf(w, "  %s  %s%s\n", e.At.Local().Format("Mon 2 Jan 15:04"), what, where)
	}
	return nil
}
