package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Coder8124/brain/internal/index"
	"github.com/Coder8124/brain/internal/ingest"
	"github.com/Coder8124/brain/internal/session"
	"github.com/Coder8124/brain/internal/text"
	"github.com/Coder8124/brain/internal/transcript"
	"github.com/Coder8124/brain/internal/vault"
)

// runIngest is `brain ingest`: read other coding agents' transcripts on this
// machine and turn each into a checkpoint *candidate* in the vault. It never
// replays a transcript and never copies one in — only a distillate plus a
// pointer back to the source. See plans/plan0-4-7.md and internal/ingest.
func runIngest(args []string) error {
	if len(args) >= 1 && args[0] == "review" {
		return runIngestReview(args[1:])
	}

	dryRun := hasFlag(args, "--dry-run")
	allProjects := hasFlag(args, "--all-projects")
	assumeYes := hasFlag(args, "--yes")
	harness := flagStr(args, "--harness", "")
	explicit := flagStr(args, "--path", "")

	rest := positionals(args, "--dry-run", "--all-projects", "--yes")
	rest = dropFlag(dropFlag(rest, "--harness"), "--path")
	wantProject := ""
	if len(rest) > 0 {
		wantProject = rest[0]
	}

	v := vaultPath()
	if _, err := os.Stat(v); err != nil {
		return missingVaultError(v)
	}

	// Reading transcripts is consent-gated: they routinely hold pasted secrets
	// and customer data the user never meant to persist anywhere. --dry-run
	// writes nothing to the vault, so it needs no grant.
	if !dryRun {
		if err := ensureIngestConsent(v, assumeYes); err != nil {
			return err
		}
	}

	sessions, readErrs, err := collectSessions(harness, explicit)
	if err != nil {
		return err
	}

	// Announce what was read and what was skipped, with numbers (invariant 3).
	read, queued, skippedDup, skippedProj, redacted := 0, 0, 0, 0, 0
	var ix *index.Index
	for _, s := range sessions {
		read++
		c := ingest.Harvest(s)
		if !allProjects && wantProject != "" && !projectMatches(c.Project, wantProject) {
			skippedProj++
			continue
		}
		if dryRun {
			fmt.Printf("  would ingest  %s  %s session %s  (%d turns, %d cmds, %d files)\n",
				orUnattributed(c.Project), c.Harness, shortID(c.SessionID), c.TurnCount, len(c.Commands), len(c.Files))
			queued++
			continue
		}
		res, err := ingest.Put(v, c)
		if err != nil {
			return fmt.Errorf("writing candidate for %s session %s: %w", c.Harness, c.SessionID, err)
		}
		if !res.Written {
			skippedDup++
			continue
		}
		queued++
		fmt.Printf("  queued  %s  %s\n", orUnattributed(c.Project), res.Path)
		if len(res.Redactions) > 0 {
			redacted += len(res.Redactions)
			for _, r := range res.Redactions {
				fmt.Printf("    redacted  %s: %s\n", r.Field, r.Reason)
			}
		}
		if ix == nil {
			if ix, err = openEvents(); err != nil {
				return err
			}
			defer ix.Close()
		}
	}

	for _, e := range readErrs {
		// Named and skipped, never swallowed (invariant 4).
		fmt.Printf("  skipped  %s\n", e)
	}

	if ix != nil {
		if _, err := ix.Sync(); err != nil {
			return fmt.Errorf("candidates written to the vault but the index did not refresh: %w", err)
		}
	}

	switch {
	case dryRun:
		fmt.Printf("\n%d transcript(s) read · %d would be queued · %d skipped (other project) · %d unreadable · nothing written\n",
			read, queued, skippedProj, len(readErrs))
	default:
		fmt.Printf("\n%d transcript(s) read · %d queued · %d already ingested · %d skipped (other project) · %d unreadable\n",
			read, queued, skippedDup, skippedProj, len(readErrs))
		if redacted > 0 {
			fmt.Printf("%d secret-shaped token(s) redacted before writing\n", redacted)
		}
		if queued > 0 {
			fmt.Println("review them:  brain ingest review")
		}
	}
	return nil
}

func runIngestReview(args []string) error {
	v := vaultPath()
	if _, err := os.Stat(v); err != nil {
		return missingVaultError(v)
	}

	if ref := flagStr(args, "--promote", ""); ref != "" {
		return ingestDecide(v, ref, true)
	}
	if ref := flagStr(args, "--reject", ""); ref != "" {
		return ingestDecide(v, ref, false)
	}

	pending, skipped := ingest.Pending(v)
	// A candidate file we could not read is named with a count, never dropped in
	// silence (invariants 3 and 4).
	if len(skipped) > 0 {
		fmt.Printf("%d candidate file(s) skipped:\n", len(skipped))
		for _, s := range skipped {
			fmt.Printf("  %s\n", s)
		}
		fmt.Println()
	}
	if len(pending) == 0 {
		fmt.Println("no ingested candidates pending review")
		return nil
	}
	fmt.Printf("%d candidate(s) pending review:\n\n", len(pending))
	for _, c := range pending {
		fmt.Printf("  %s  %s session %s\n", orUnattributed(c.Project), c.Harness, shortID(c.SessionID))
		fmt.Printf("      %d turns · %d commands · %d files", c.TurnCount, len(c.Commands), len(c.Files))
		if c.Skipped > 0 {
			fmt.Printf(" · %d unparsed lines", c.Skipped)
		}
		fmt.Printf(" · %s\n", c.Tier)
		fmt.Printf("      promote:  brain ingest review --promote %s\n\n", shortID(c.SessionID))
	}
	return nil
}

func ingestDecide(v, ref string, promote bool) error {
	c, abs, ok, err := ingest.Find(v, ref)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("no candidate matches %q — run `brain ingest review` for the list", ref)
	}
	if !promote {
		if err := ingest.Reject(abs, c); err != nil {
			return err
		}
		fmt.Printf("rejected %s session %s — it will not be offered again\n", c.Harness, shortID(c.SessionID))
		return nil
	}

	ix, err := openEvents()
	if err != nil {
		return err
	}
	defer ix.Close()
	if err := session.Init(ix.DB); err != nil {
		return err
	}
	cp, err := ingest.Promote(ix.DB, v, c, abs)
	// Promote returns a non-nil cp with a non-nil err when the checkpoint was
	// durably written but flipping the candidate's status failed. Discarding cp
	// here would hide a real promotion (invariant 3) and leave the candidate
	// pending, so the next review re-offers it and writes a duplicate checkpoint.
	if cp == nil {
		return err
	}
	fmt.Printf("promoted to checkpoint: %s.md\n", cp.Slug)
	fmt.Printf("`brain resume %s` picks it up now\n", cp.Project)
	if _, syncErr := ix.Sync(); syncErr != nil {
		return fmt.Errorf("checkpoint %s written but the index did not refresh: %w", cp.Slug, syncErr)
	}
	if err != nil {
		fmt.Printf("warning: %v\n", err)
		fmt.Printf("         run `brain ingest review --reject %s` or it is offered again\n", shortID(c.SessionID))
	}
	return nil
}

// collectSessions reads every discoverable transcript, or one explicit file.
// The second return is the list of per-file skip reasons — reported, not
// swallowed.
func collectSessions(harness, explicit string) ([]*transcript.Session, []string, error) {
	if explicit != "" {
		if harness == "" {
			return nil, nil, fmt.Errorf("--path needs --harness to say how to read the file")
		}
		s, err := transcript.ReadFile(harness, explicit)
		if err != nil {
			return nil, []string{err.Error()}, nil
		}
		return []*transcript.Session{s}, nil, nil
	}

	harnesses := transcript.Harnesses()
	if harness != "" {
		harnesses = []string{harness}
	}

	var out []*transcript.Session
	var skips []string
	for _, h := range harnesses {
		paths, err := transcript.Sessions(h)
		if err != nil {
			// A harness with no directory / no txcript is only worth naming when
			// the user asked for it by name; otherwise it is just noise.
			if harness != "" {
				skips = append(skips, err.Error())
			}
			continue
		}
		for _, p := range paths {
			s, err := transcript.ReadFile(h, p)
			if err != nil {
				skips = append(skips, fmt.Sprintf("%s: %s: %v", h, filepath.Base(p), err))
				continue
			}
			out = append(out, s)
		}
	}
	// Ended is 0 for a transcript with no timestamped final turn, and 0 > 0 is
	// false for every pair, so the sort fell back to read order — the same list
	// printed differently run to run. Started and then the file path break the
	// tie deterministically.
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Ended != b.Ended {
			return a.Ended > b.Ended
		}
		if a.Started != b.Started {
			return a.Started > b.Started
		}
		return a.Path < b.Path
	})
	return out, skips, nil
}

// --- consent marker ---------------------------------------------------------

type ingestConsent struct {
	Granted string `json:"granted"` // RFC3339
}

func ingestConsentPath(vaultDir string) string {
	return ingest.ConsentPath(vaultDir)
}

// ensureIngestConsent records, once per machine, that the user agreed to let
// ingest read other agents' transcript files. The marker lives in .brain/ (not
// the vault proper) — like the web-bridge pairing token, it is machine-local
// state, not something the vault-is-truth rebuild promise covers. Deleting it
// just means being asked again.
func ensureIngestConsent(vaultDir string, assumeYes bool) error {
	p := ingestConsentPath(vaultDir)
	if b, err := os.ReadFile(p); err == nil {
		var c ingestConsent
		if json.Unmarshal(b, &c) == nil && c.Granted != "" {
			return nil
		}
	}

	if !assumeYes {
		fmt.Println("`brain ingest` reads other coding agents' session transcripts from disk")
		fmt.Println("(Claude Code under ~/.claude/projects, Codex under ~/.codex/sessions, and")
		fmt.Println("others via txcript). Only a distilled checkpoint candidate is written to the")
		fmt.Println("vault — never the transcript itself. This is asked once.")
		fmt.Print("\nallow ingest to read transcripts on this machine? [y/N] ")
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if a := strings.TrimSpace(strings.ToLower(line)); a != "y" && a != "yes" {
			return fmt.Errorf("ingest needs consent to read transcripts; not granted")
		}
	}

	b, err := json.MarshalIndent(ingestConsent{Granted: time.Now().UTC().Format(time.RFC3339)}, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding ingest consent: %w", err)
	}
	if err := vault.WriteAtomic(p, b); err != nil {
		return fmt.Errorf("recording ingest consent: %w", err)
	}
	fmt.Println("consent recorded in .brain/ingest-consent.json")
	return nil
}

// --- small helpers ---------------------------------------------------------

// positionals returns args with the named boolean flags removed, so the
// remaining non-flag words are the command's positionals.
func positionals(args []string, boolFlags ...string) []string {
	out := make([]string, 0, len(args))
	skip := map[string]bool{}
	for _, f := range boolFlags {
		skip[f] = true
	}
	for _, a := range args {
		if skip[a] {
			continue
		}
		out = append(out, a)
	}
	return out
}

func projectMatches(candidate, want string) bool {
	if candidate == "" {
		return false
	}
	return strings.EqualFold(candidate, want) || strings.EqualFold(filepath.Base(candidate), want)
}

func orUnattributed(p string) string {
	if strings.TrimSpace(p) == "" {
		return "(unattributed)"
	}
	return p
}

func shortID(id string) string {
	return text.Truncate(id, 12)
}

// ingestPendingCount is used by `brain resume` to mention the queue. A read
// failure is not worth surfacing there — the dedicated command will report it.
func ingestPendingCount(vaultDir string) int {
	pending, _ := ingest.Pending(vaultDir)
	return len(pending)
}
