package main

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/Coder8124/logos/internal/activity"
	"github.com/Coder8124/logos/internal/claims"
	"github.com/Coder8124/logos/internal/deadend"
	"github.com/Coder8124/logos/internal/gitstate"
	"github.com/Coder8124/logos/internal/memory"
	"github.com/Coder8124/logos/internal/procedure"
	usagepkg "github.com/Coder8124/logos/internal/usage"
)

// checkWindow is how far a dead-end check's ledger line may sit from the
// activity row of the tool call that made it. The ledger is written while the
// tool runs and the row after it returns, so seconds apart; a minute allows a
// slow check without reaching the next one.
const checkWindow = 60

// avoidedReceipt is the session-end "may have avoided" receipt: the rulings
// this session was handed — by its resume, or by a before_you_try or `logos
// tried` it ran — that name a file, where the session then touched none of
// the files they name. Each is recorded in the ledger once and returned for
// the hook to say.
//
// It is mechanical on purpose. Logos cannot see whether an agent changed its
// mind, only what it was shown and what it then edited, so the claim is
// "may have avoided", and a ruling that names no file is never claimed:
// staying away from nothing proves nothing.
//
// What the session was shown is found two ways, because the two paths know
// different things. The SessionStart hook's resume is given the session id
// (LOGOS_SESSION) and records it. An MCP server serves the whole session and
// is never told its id, so its checks are matched to this session's own
// activity rows for the same tool call, by time.
func avoidedReceipt(vault string, db *sql.DB, project, sessionID string, events []activity.Event) ([]deadend.Ruling, error) {
	if sessionID == "" || !usagepkg.Recording(vault) {
		return nil, nil
	}
	ledger, _, err := usagepkg.Read(vault)
	if err != nil {
		return nil, err
	}
	shown := map[string]bool{}
	already := map[string]bool{}
	used := map[int]bool{}
	for i, e := range ledger {
		switch {
		case e.Session != sessionID:
		case e.Kind == usagepkg.KindAvoided:
			// The session was resumed and ended again: what it was already
			// credited with is not counted twice.
			for _, r := range e.Refs {
				already[r] = true
			}
		case e.Kind == usagepkg.KindPack:
			used[i] = true
			for _, r := range e.Refs {
				shown[r] = true
			}
		}
	}
	touched := map[string]bool{}
	// blind is an edit whose path the activity row cut short. Which file it
	// was is gone with the tail, so it may have been any file a ruling names,
	// and staying away from them is not something this session can be shown
	// to have done.
	blind := false
	for _, a := range events {
		if a.Kind != activity.KindTool {
			continue
		}
		if f := strings.TrimPrefix(a.Summary, a.Tool+" "); f != a.Summary && isWork(a.Tool) && a.Tool != "Bash" {
			if strings.HasSuffix(f, "…") {
				blind = true
			}
			touched[filepath.ToSlash(f)] = true
		}
		via := checkVia(a)
		if via == "" {
			continue
		}
		best := -1
		// The project as well as the time: a session on another project can
		// run the same check in the same minute.
		for i, e := range ledger {
			if used[i] || e.Kind != usagepkg.KindDeadEnd || e.Via != via || !usagepkg.SameProject(e.Project, project) ||
				e.TS > a.TS+5 || e.TS < a.TS-checkWindow {
				continue
			}
			if best < 0 || abs64(e.TS-a.TS) < abs64(ledger[best].TS-a.TS) {
				best = i
			}
		}
		if best >= 0 {
			used[best] = true
			for _, r := range ledger[best].Refs {
				shown[r] = true
			}
		}
	}
	if len(shown) == 0 || blind {
		return nil, nil
	}

	rulings, err := deadend.Collect(vault, db, "")
	if err != nil {
		return nil, err
	}
	var out []deadend.Ruling
	var refs []string
	seen := map[string]bool{}
	for _, r := range rulings {
		ref := usagepkg.Ref(r.Record.Raw)
		if !shown[ref] || already[ref] || seen[ref] {
			continue
		}
		seen[ref] = true
		named := gitstate.NamedFiles(r.Record.Raw)
		if len(named) == 0 || touchedAny(touched, named) {
			continue
		}
		out = append(out, r)
		refs = append(refs, ref)
	}
	if len(out) == 0 {
		return nil, nil
	}
	err = usagepkg.Record(vault, usagepkg.Event{
		Kind: usagepkg.KindAvoided, Via: "hook:session-end", Project: project, Session: sessionID,
		Rulings: len(out), Refs: refs,
	})
	return out, err
}

// provenReceipt is the session-end "procedure verified" receipt: procedures a
// before_you_try in this session handed back whose verify then ran, and
// passed, later in the same session. Each is recorded in the ledger as proven
// once a session, which is where before_you_try reads its evidence from.
//
// The match is as mechanical as avoidedReceipt's and for the same reason —
// the log holds program and first argument of each command, so "go test
// ./internal/auth" is met by any passing go test after the procedure was
// shown. That is weaker than the verify itself, and the receipt says only
// that the verify passed, not that the route was followed.
//
// Procedures are memories, so without the index there is nothing to resolve
// a ledger ref to, and nothing is claimed.
func provenReceipt(vault string, db *sql.DB, project, sessionID string, events []activity.Event) ([]procedure.Record, error) {
	if sessionID == "" || db == nil || !usagepkg.Recording(vault) {
		return nil, nil
	}
	ledger, _, err := usagepkg.Read(vault)
	if err != nil {
		return nil, err
	}
	already := map[string]bool{}
	for _, e := range ledger {
		if e.Session == sessionID && e.Kind == usagepkg.KindProven {
			for _, r := range e.Refs {
				already[r] = true
			}
		}
	}
	// shownAt is when each procedure was first handed back in this session:
	// a verify that passed before it was shown is not evidence for it.
	shownAt := map[string]int64{}
	used := map[int]bool{}
	for _, a := range events {
		if checkVia(a) != "mcp:before_you_try" {
			continue
		}
		best := -1
		for i, e := range ledger {
			if used[i] || e.Kind != usagepkg.KindProcedure || e.Via != "mcp:before_you_try" || !usagepkg.SameProject(e.Project, project) ||
				e.TS > a.TS+5 || e.TS < a.TS-checkWindow {
				continue
			}
			if best < 0 || abs64(e.TS-a.TS) < abs64(ledger[best].TS-a.TS) {
				best = i
			}
		}
		if best < 0 {
			continue
		}
		used[best] = true
		for _, r := range ledger[best].Refs {
			if t, ok := shownAt[r]; !ok || a.TS < t {
				shownAt[r] = a.TS
			}
		}
	}
	if len(shownAt) == 0 {
		return nil, nil
	}

	corpus, err := memory.RecallProcedures(db, nil, "", "", 0)
	if err != nil {
		return nil, err
	}
	var out []procedure.Record
	var refs []string
	for _, m := range corpus {
		ref := usagepkg.Ref(m.Text)
		at, ok := shownAt[ref]
		if !ok || already[ref] {
			continue
		}
		already[ref] = true
		rec := procedure.ParseRecord(m.Text)
		if rec.Verify == "" || !claims.Passed(rec.Verify, events, sessionID, at) {
			continue
		}
		out = append(out, rec)
		refs = append(refs, ref)
	}
	if len(out) == 0 {
		return nil, nil
	}
	err = usagepkg.Record(vault, usagepkg.Event{
		Kind: usagepkg.KindProven, Via: "hook:session-end", Project: project, Session: sessionID, Refs: refs,
	})
	return out, err
}

// renderProven is the proven receipt, one line a procedure. Only a procedure
// recorded as once changes what before_you_try calls it, so only that one is
// told it moved.
func renderProven(rs []procedure.Record) string {
	var b strings.Builder
	for _, r := range rs {
		fmt.Fprintf(&b, "procedure verified: %s — its verify passed after it was shown", terminalSafe(r.Route, 120))
		if r.Evidence == procedure.EvidenceOnce {
			b.WriteString(" (once → verified)")
		}
		b.WriteString("\n")
	}
	return b.String()
}

// checkVia is the ledger Via a tool call's dead-end check was recorded under,
// or "" when the call was not one the receipt counts. `why` is left out: it
// answers what was decided about a file someone came to look at, not whether
// an approach they proposed was tried.
func checkVia(a activity.Event) string {
	switch {
	case strings.HasSuffix(a.Tool, "__before_you_try"):
		return "mcp:before_you_try"
	case a.Tool == "Bash" && strings.Contains(a.Summary, "logos tried ") && !strings.Contains(a.Summary, "--ruled-out"):
		return "cli:tried"
	}
	return ""
}

// touchedAny reports whether the session edited a file the ruling names. A
// named path is repository-relative and an edit's is usually absolute, so a
// path matches on its tail; a bare name on the file's own name.
func touchedAny(touched map[string]bool, named []string) bool {
	for f := range touched {
		for _, n := range named {
			if f == n || strings.HasSuffix(f, "/"+n) || (!strings.Contains(n, "/") && filepath.Base(f) == n) {
				return true
			}
		}
	}
	return false
}

// renderAvoided is the receipt as the SessionEnd hook relays it, one line a
// ruling. The route is vault content, shown to the person, never obeyed.
func renderAvoided(rs []deadend.Ruling) string {
	var b strings.Builder
	for _, r := range rs {
		who := r.Agent
		if who == "" {
			who = "an earlier session"
		}
		fmt.Fprintf(&b, "may have avoided: %s (ruled out by %s, %s)\n",
			terminalSafe(r.Text, 120), terminalSafe(who, 40), time.Unix(r.When, 0).Format("2 Jan 2006"))
	}
	return b.String()
}

// terminalSafe is s cut to n runes with control characters gone: the hook
// prints it straight to the user's terminal, and an escape sequence in
// someone's ruling would be obeyed by it.
func terminalSafe(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return ' '
		}
		return r
	}, s)
	if rs := []rune(s); len(rs) > n {
		return string(rs[:n]) + "…"
	}
	return s
}

func abs64(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

// sessionReceipt is the receipt for the session whose SessionEnd payload is
// raw, as the lines the hook relays. A receipt that could not be worked out
// says so in a line of its own rather than vanishing: the checkpoint beside it
// is the hook's real job and goes ahead either way.
func sessionReceipt(vault, project string, raw []byte) string {
	end, err := activity.FromHook("SessionEnd", raw, project)
	if err != nil || end.Session == "" {
		return ""
	}
	events, err := sessionEvents(vault, project, end.Session)
	if err != nil {
		return fmt.Sprintf("receipt not worked out: %v\n", err)
	}
	// The index only for rulings still in working notes; without it the
	// checkpointed ones are found all the same.
	var db *sql.DB
	if ix, err := openIndexQuiet(); err == nil {
		defer ix.Close()
		db = ix.DB
	}
	var out strings.Builder
	rs, err := avoidedReceipt(vault, db, project, end.Session, events)
	if err != nil {
		fmt.Fprintf(&out, "receipt not worked out: %v\n", err)
	} else {
		out.WriteString(renderAvoided(rs))
	}
	ps, err := provenReceipt(vault, db, project, end.Session, events)
	out.WriteString(renderProven(ps))
	switch {
	case err != nil && len(ps) > 0:
		fmt.Fprintf(&out, "these were not added to the usage ledger, so before_you_try will not count them: %v\n", err)
	case err != nil:
		fmt.Fprintf(&out, "procedure receipt not worked out: %v\n", err)
	}
	return out.String()
}
