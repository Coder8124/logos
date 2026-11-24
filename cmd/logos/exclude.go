package main

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Coder8124/logos/internal/exclude"
)

const excludeUsage = `usage: logos exclude --project PATH        never read the sessions run in PATH (and below it)
       logos exclude --session ID          never read that one chat
       logos exclude --session ID --erase  also delete what is already kept of it
       logos exclude --list                what is excluded, and any pause
       logos exclude --remove PATH|ID      read it again`

// runExclude is the person's way to say "never read this conversation". It
// writes the vault (exclude.md) and nothing else except, for --erase, the
// removal of what was already kept.
func runExclude(args []string) error {
	vault, err := requireVault()
	if err != nil {
		return err
	}
	project := flagStr(args, "--project", "")
	sess := flagStr(args, "--session", "")
	remove := flagStr(args, "--remove", "")
	erase := hasFlag(args, "--erase")

	switch {
	case hasFlag(args, "--list"):
		return excludeList(vault)
	case remove != "":
		kind, err := exclude.Remove(vault, remove)
		if err != nil {
			return err
		}
		fmt.Printf("%s no longer excluded: %s — its sessions are read again from the next sweep\n", kind, remove)
		return nil
	case project != "" && sess != "":
		return errors.New("name a project or a session, not both\n" + excludeUsage)
	case erase && sess == "":
		return errors.New("--erase removes one chat's kept copies, so it needs --session ID\n" + excludeUsage)
	case project != "":
		stored, err := exclude.AddProject(vault, project)
		if err != nil {
			return err
		}
		fmt.Printf("excluded project %s — its sessions, and those in any folder under it, are not read; no activity rows or saved plans are written for them either\n", stored)
		k, root := exclude.KeptForProject(vault, stored)
		fmt.Printf("already kept: %d archived transcript(s), %d checkpoint(s)\n", k.Transcripts, k.Checkpoints)
		if k.Transcripts+k.Checkpoints > 0 {
			fmt.Printf("exclusion does not delete them; to remove them, delete %s and run `logos index`\n", root)
		}
		return nil
	case sess != "":
		if erase {
			// Resolved before anything is written: an id that fits two
			// conversations erases neither, and adds no exclusion either.
			if _, err := exclude.Resolve(vault, sess); err != nil {
				return err
			}
		}
		if err := exclude.AddSession(vault, sess); err != nil {
			return err
		}
		fmt.Printf("excluded session %s — it is not read again, and writes no activity rows or saved plans\n", sess)
		if !erase {
			k, err := exclude.KeptForSession(vault, sess)
			var amb *exclude.Ambiguous
			if errors.As(err, &amb) {
				// The exclusion matches by prefix, so it covers every one of
				// them; only counting and erasing need one answer.
				fmt.Printf("it covers %d kept conversations: %s\n", len(amb.Matches), strings.Join(amb.Matches, ", "))
				fmt.Println("exclusion does not delete them; `--erase` with one full id removes that conversation's copies")
				return nil
			}
			if err != nil {
				return err
			}
			fmt.Printf("already kept: %d archived transcript(s), %d checkpoint(s)\n", k.Transcripts, k.Checkpoints)
			if k.Transcripts+k.Checkpoints > 0 {
				fmt.Printf("exclusion does not delete them; `logos exclude --session %s --erase` removes them\n", sess)
			}
			return nil
		}
		return eraseSession(vault, sess)
	}
	return errors.New(excludeUsage)
}

// eraseSession removes the vault files first and syncs the index second, so a
// crash between the two leaves a stale cache the next index fixes. The sync
// runs even when the removal failed part way, so the cache matches what is
// left, and the failure is still returned.
func eraseSession(vault, id string) error {
	k, eraseErr := exclude.Erase(vault, id)
	ix, err := openIndexQuiet()
	if err != nil {
		return errors.Join(eraseErr, fmt.Errorf("the vault is changed but the index could not be opened: %w (run `logos index`)", err))
	}
	defer ix.Close()
	if _, err := ix.Sync(); err != nil {
		eraseErr = errors.Join(eraseErr, fmt.Errorf("the vault is changed but the index did not sync: %w (run `logos index`)", err))
	}
	if eraseErr != nil {
		fmt.Printf("erased before failing: %d archived transcript(s), %d checkpoint(s)\n", k.Transcripts, k.Checkpoints)
		return eraseErr
	}
	fmt.Printf("erased %d archived transcript(s) and %d checkpoint(s) of session %s\n", k.Transcripts, k.Checkpoints, id)
	return nil
}

func excludeList(vault string) error {
	r, err := exclude.Load(vault)
	if err != nil {
		return err
	}
	now := time.Now()
	if w, ok := r.ActivePause(now); ok {
		fmt.Printf("paused until %s (%s left) — `logos pause resume` ends it\n",
			w.Until.Local().Format("15:04"), w.Until.Sub(now).Round(time.Minute))
	}
	for _, b := range r.Broken {
		fmt.Printf("BROKEN %s — until it is fixed nothing is read; correct its times (from=… until=… as RFC 3339) or delete it in %s\n", b, exclude.Path(vault))
	}
	if len(r.Projects)+len(r.Sessions) == 0 {
		fmt.Println("nothing is excluded — `logos exclude --project PATH` or `--session ID` adds one")
		return nil
	}
	fmt.Printf("%d project(s), %d session(s) excluded (%s)\n", len(r.Projects), len(r.Sessions), exclude.Path(vault))
	for _, p := range r.Projects {
		fmt.Println("  project", p)
	}
	for _, s := range r.Sessions {
		fmt.Println("  session", s)
	}
	return nil
}

const pauseUsage = "usage: logos pause [DURATION]   stop reading sessions for a while (default 1h; 30m, 2h, 1d)\n       logos pause resume       end the pause early"

// runPause opens or ends a pause window. Sessions active during a window are
// skipped by harvest, sweep and archive, and the command that skipped them
// says how many.
func runPause(args []string) error {
	vault, err := requireVault()
	if err != nil {
		return err
	}
	if len(args) > 1 {
		return errors.New(pauseUsage)
	}
	now := time.Now()
	if len(args) == 1 && args[0] == "resume" {
		n, err := exclude.Resume(vault, now)
		if err != nil {
			return err
		}
		if n == 0 {
			fmt.Println("no pause is in force — nothing to resume")
			return nil
		}
		fmt.Printf("pause ended — %d window(s); sessions active during it stay skipped\n", n)
		return nil
	}
	d := time.Hour
	if len(args) == 1 {
		if d, err = parsePause(args[0]); err != nil {
			return err
		}
	}
	w, err := exclude.Pause(vault, d, now)
	if err != nil {
		return err
	}
	fmt.Printf("paused until %s — sessions active before then are not read, and no activity rows or plans are written; `logos pause resume` ends it\n", w.Until.Local().Format("15:04"))
	return nil
}

// parsePause takes Go durations and a day count ("1d"), the unit a person
// reaches for when pausing overnight.
func parsePause(s string) (time.Duration, error) {
	if n, ok := strings.CutSuffix(s, "d"); ok {
		var days int
		if _, err := fmt.Sscanf(n, "%d", &days); err == nil && days > 0 && fmt.Sprint(days) == n {
			return time.Duration(days) * 24 * time.Hour, nil
		}
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%q is not a length of time (try 30m, 2h, 1d)\n%s", s, pauseUsage)
	}
	return d, nil
}
