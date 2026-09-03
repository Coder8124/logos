package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Coder8124/brain/internal/memory"
	"github.com/Coder8124/brain/internal/router"
)

// memoryCmd inspects and edits the assistant's persistent memory — the facts it
// has learned about the user across sessions.
func memoryCmd(args []string) error {
	ix, err := openEvents()
	if err != nil {
		return err
	}
	defer ix.Close()
	if err := memory.Init(ix.DB); err != nil {
		return err
	}

	if len(args) == 0 {
		mems, err := memory.All(ix.DB)
		if err != nil {
			return err
		}
		if len(mems) == 0 {
			fmt.Println("no memories yet — the assistant learns as you talk to it.")
			return nil
		}
		fmt.Printf("%d memories\n\n", len(mems))
		for _, m := range mems {
			meta := ""
			if m.Uses > 0 {
				meta += fmt.Sprintf(" · recalled %d×", m.Uses)
			}
			if m.Project != "" {
				meta += " · " + m.Project
			}
			// Pin state has to show up here or "pin" is invisible the moment you
			// close the terminal that set it — the same reason abandonment and
			// continuity surface where they'll actually be seen.
			switch m.Pin {
			case memory.PinAlways:
				meta += " · pinned"
			case memory.PinNever:
				meta += " · excluded"
			}
			// salience = how much it matters; conf = how sure we are it's true.
			fmt.Printf("  [%d] (%-10s sal %.2f · conf %s) %s%s · %s\n",
				m.ID, m.Kind, m.Salience, confBar(m.Confidence), m.Text, meta, agentLabel(m.Agent))
		}
		return nil
	}

	switch args[0] {
	case "log":
		return memoryLog(ix.DB, strings.TrimSpace(flagStr(args, "--project", "")), flagInt(args, "--n", 40))
	case "history":
		id := parseID(args)
		if id == 0 {
			return fmt.Errorf("usage: brain memory history <id>")
		}
		return memoryHistory(ix.DB, id)
	case "graph":
		return memoryGraph(ix.DB, args)
	case "diff":
		return memoryDiff(ix.DB, args)
	case "health":
		return memoryHealth(ix.DB)
	case "add":
		project := strings.TrimSpace(flagStr(args, "--project", ""))
		// The flag and its value have to come out of the words, or they end up
		// *inside* the fact: "the build runs on arm only --project harrier" was
		// stored verbatim, and the memory then belonged to no project while
		// reading as though it did.
		text := strings.TrimSpace(strings.Join(dropFlag(args[1:], "--project"), " "))
		if text == "" {
			return fmt.Errorf("usage: brain memory add <fact> [--project <name>]")
		}
		rt, err := openRouter()
		if err != nil {
			return err
		}
		embed, _ := rt.Model(router.T0)
		r, err := memory.Store(ix.DB, rt.Local(), embed, &memory.Memory{
			Text: text, Kind: memory.Fact, Salience: 0.7, Source: "manual",
			Project: project, Created: time.Now().Unix(),
		})
		if err != nil {
			return err
		}
		// Which of the three things happened, not just "done". Storing a fact
		// the vault already held is a different outcome from learning a new
		// one, and a user who cannot tell them apart will keep re-adding.
		where := ""
		if project != "" {
			where = " in " + project
		}
		switch {
		case r.Created():
			fmt.Printf("remembered%s — memory #%d.\n", where, r.ID)
		case r.Outcome == memory.EvReinforced:
			fmt.Printf("already knew that%s — reinforced memory #%d.\n", where, r.Ref)
		default:
			fmt.Println("nothing to remember.")
		}
	case "consolidate":
		rt, err := openRouter()
		if err != nil {
			return err
		}
		d, _ := memory.Decay(ix.DB, time.Now().Unix())
		m, s, err := memory.Consolidate(ix.DB, rt)
		if err != nil {
			return err
		}
		fmt.Printf("decayed %d · merged %d duplicates · superseded %d outdated\n", d, m, s)
		return nil
	case "forget":
		// --source undoes a whole arrival at once, which is what a bulk seeding
		// like `brain bootstrap` needs: the alternative is asking the user to
		// work out which ids were the machine's.
		if src := strings.TrimSpace(flagStr(args, "--source", "")); src != "" {
			n, err := memory.ForgetBySource(ix.DB, src)
			if err != nil {
				return err
			}
			fmt.Printf("forgot %d memories learned from %s.\n", n, src)
			return nil
		}
		id := parseID(args)
		if id == 0 {
			return fmt.Errorf("usage: brain memory forget <id> | --source <name>")
		}
		if err := memory.Forget(ix.DB, id); err != nil {
			return err
		}
		fmt.Println("forgotten.")
	case "pin":
		id := parseID(args)
		if id == 0 {
			return fmt.Errorf("usage: brain memory pin <id>")
		}
		if err := memory.Pin(ix.DB, id); err != nil {
			return err
		}
		fmt.Println("pinned — always included in context packs, budget permitting.")
	case "unpin":
		id := parseID(args)
		if id == 0 {
			return fmt.Errorf("usage: brain memory unpin <id>")
		}
		if err := memory.Unpin(ix.DB, id); err != nil {
			return err
		}
		fmt.Println("unpinned — back to normal ranking.")
	case "exclude":
		id := parseID(args)
		if id == 0 {
			return fmt.Errorf("usage: brain memory exclude <id>")
		}
		if err := memory.Exclude(ix.DB, id); err != nil {
			return err
		}
		fmt.Println("excluded — kept on record, never surfaced. `brain memory unpin` to reverse.")
	default:
		return fmt.Errorf("usage: brain memory [add <fact> [--project P] | forget <id> | pin <id> | unpin <id> | exclude <id> | log [--project P] | history <id> | diff | health | consolidate]")
	}
	return nil
}

// memoryDiff answers "what changed?" over a window, optionally about a subject.
//
//	brain memory diff [subject…] [--since YYYY-MM-DD] [--until YYYY-MM-DD] [--days N]
//
// Instant and offline — it reads the memory timeline, no model.
func memoryDiff(db *sql.DB, args []string) error {
	// Everything that isn't a flag or a flag value is the subject.
	subject := strings.TrimSpace(strings.Join(nonFlagArgs(args[1:]), " "))

	days := flagInt(args, "--days", 7)
	until := time.Now()
	since := until.AddDate(0, 0, -days)
	if v := flagStr(args, "--since", ""); v != "" {
		t, err := time.ParseInLocation("2006-01-02", v, time.Local)
		if err != nil {
			return fmt.Errorf("bad --since %q, want YYYY-MM-DD", v)
		}
		since = t
	}
	if v := flagStr(args, "--until", ""); v != "" {
		t, err := time.ParseInLocation("2006-01-02", v, time.Local)
		if err != nil {
			return fmt.Errorf("bad --until %q, want YYYY-MM-DD", v)
		}
		until = t.AddDate(0, 0, 1) // inclusive of the named day
	}

	res, err := memory.Diff(db, subject, since.Unix(), until.Unix())
	if err != nil {
		return err
	}

	head := fmt.Sprintf("what changed · %s → %s", since.Format("Jan 02"), until.Format("Jan 02"))
	if subject != "" {
		head = fmt.Sprintf("what changed about %q · %s → %s", subject, since.Format("Jan 02"), until.Format("Jan 02"))
	}
	fmt.Println(head)

	if res.Empty() {
		fmt.Println("\n  nothing changed in that window.")
		return nil
	}
	fmt.Println()

	printDiffBucket("+", res.Added)
	printDiffBucket("-", res.Removed)
	printDiffBucket("~", res.Corroborated) // corroborated / firmed up
	return nil
}

func printDiffBucket(sign string, entries []memory.DiffEntry) {
	for _, e := range entries {
		when := time.Unix(e.TS, 0).Format("Jan 02")
		fmt.Printf("  %s %s  %s\n", sign, when, truncateLine(e.Text, 64))
	}
}

// memoryHealth prints the store's diagnostic — git status for what it knows.
func memoryHealth(db *sql.DB) error {
	rep, err := memory.Health(db)
	if err != nil {
		return err
	}
	if rep.Total == 0 {
		fmt.Println("no memories yet — nothing to diagnose.")
		return nil
	}

	fmt.Printf("Memory health · %.0f%%  %s\n\n", rep.Score*100, confBar(rep.Score))
	fmt.Printf("  %d memories\n", rep.Total)

	clean := rep.Total - flaggedCount(rep)
	fmt.Printf("  ✓ %d clean\n", clean)
	if rep.Duplicates > 0 {
		fmt.Printf("  ⚠ %d duplicate memories\n", rep.Duplicates)
	}
	if rep.Stale > 0 {
		fmt.Printf("  ⚠ %d stale facts\n", rep.Stale)
	}
	if rep.LowConfidence > 0 {
		fmt.Printf("  ⚠ %d low-confidence hunches\n", rep.LowConfidence)
	}
	if rep.Orphans > 0 {
		fmt.Printf("  · %d orphans (linked to no note)\n", rep.Orphans)
	}

	if rep.Duplicates > 0 {
		fmt.Println("\ntip: `brain memory consolidate` folds duplicates and retires stale facts")
	}
	return nil
}

// flaggedCount is how many memories carry a score-affecting defect (dupes, stale,
// low-confidence). Orphans are informational and excluded, matching Health's score.
func flaggedCount(rep memory.HealthReport) int {
	// Score = 1 - flagged/total, so flagged = round((1-score)*total).
	return int(float64(rep.Total)*(1-rep.Score) + 0.5)
}

// nonFlagArgs drops flags and their values, leaving the positional words. It
// knows the value-taking flags the diff command accepts.
func nonFlagArgs(args []string) []string {
	valueFlags := map[string]bool{"--since": true, "--until": true, "--days": true}
	var out []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if valueFlags[a] {
			i++ // skip this flag's value
			continue
		}
		if strings.HasPrefix(a, "--") {
			continue
		}
		out = append(out, a)
	}
	return out
}

// agentLabel names who learned a memory, for the listing. A dash rather than
// nothing: most memories predate this field or came from the CLI rather than
// a named MCP client, and a bare trailing "· " on those rows would read as a
// rendering bug rather than an honest "we don't know".
func agentLabel(agent string) string {
	if strings.TrimSpace(agent) == "" {
		return "-"
	}
	return agent
}

// confBar renders a confidence as a short 5-cell bar plus the number, so the
// listing reads at a glance which facts are certain and which are hunches.
func confBar(c float64) string {
	filled := int(c*5 + 0.5)
	if filled > 5 {
		filled = 5
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", 5-filled)
	return fmt.Sprintf("%s %.2f", bar, c)
}

// memoryLog prints the timeline — git history for memory, newest first. With a
// project it prints that project's timeline instead: what changed in what the
// assistant knows about this particular work.
//
// The per-project view is the one people actually ask for. A global log mixes
// every repo a person has ever opened, and at that point the honest answer to
// "what has it learned about kestrel this week" is to scroll and squint.
func memoryLog(db *sql.DB, project string, n int) error {
	var entries []memory.LogEntry
	var err error
	if project != "" {
		entries, err = memory.TimelineInProject(db, project, n)
	} else {
		entries, err = memory.Timeline(db, n)
	}
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		if project != "" {
			fmt.Printf("nothing recorded for %q yet.\n", project)
			fmt.Println("  Either no memory has been stored against that project, or it is spelled differently —")
			fmt.Println("  `brain memory log` with no --project shows every project's events together.")
			return nil
		}
		fmt.Println("no memory history yet — it fills in as the assistant learns, corroborates, and revises.")
		return nil
	}
	scope := "memory timeline"
	if project != "" {
		scope = fmt.Sprintf("memory timeline · %s", project)
	}
	fmt.Printf("%s · %d most recent events\n\n", scope, len(entries))
	for _, e := range entries {
		when := time.Unix(e.TS, 0).Format("Jan 02 15:04")
		ref := ""
		if e.RefID != 0 {
			ref = fmt.Sprintf(" (→ #%d)", e.RefID)
		}
		// The project column only earns its width in the unscoped view, where
		// it is the thing that makes a mixed log readable. In a scoped one it
		// would be the same word on every line.
		where := ""
		if project == "" && e.Project != "" {
			where = fmt.Sprintf(" [%s]", e.Project)
		}
		fmt.Printf("  %s  %-11s #%-4d %s%s%s\n", when, e.Event, e.MemID, truncateLine(e.Detail, 60), ref, where)
	}
	// Said plainly, and only when it is true. A timeline that silently omits
	// part of the past is the failure this whole system exists to avoid, so a
	// partial one announces itself rather than passing as complete.
	if project != "" {
		if n, err := memory.UnattributedCount(db); err == nil && n > 0 {
			fmt.Printf("\n%d older events carry no project and are not shown here; `brain memory log` includes them.\n", n)
		}
	}
	return nil
}

// memoryHistory prints one memory's whole life, oldest first.
func memoryHistory(db *sql.DB, id int64) error {
	entries, err := memory.History(db, id)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return fmt.Errorf("no history for memory #%d", id)
	}
	fmt.Printf("history of memory #%d\n\n", id)
	for _, e := range entries {
		when := time.Unix(e.TS, 0).Format("2006-01-02 15:04")
		ref := ""
		if e.RefID != 0 {
			ref = fmt.Sprintf(" (→ #%d)", e.RefID)
		}
		fmt.Printf("  %s  %-11s %s%s\n", when, e.Event, truncateLine(e.Detail, 70), ref)
	}
	return nil
}

// memoryGraph builds and renders the memory relationship graph — memories, the
// .md notes they mention, and how they relate.
func memoryGraph(db *sql.DB, args []string) error {
	g, err := memory.BuildGraph(db, hasFlag(args, "--similar"))
	if err != nil {
		return err
	}
	if hasFlag(args, "--mermaid") {
		fmt.Print(g.Mermaid())
		return nil
	}
	if hasFlag(args, "--json") {
		b, _ := json.MarshalIndent(g, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	if len(g.Nodes) == 0 {
		fmt.Println("the memory graph is empty — add memories and index your vault to connect them.")
		return nil
	}

	var mems, notes int
	for _, n := range g.Nodes {
		if n.Type == "note" {
			notes++
		} else {
			mems++
		}
	}
	rel := map[string]int{}
	for _, e := range g.Edges {
		rel[e.Rel]++
	}
	fmt.Printf("memory graph · %d memories, %d linked notes · %d edges (%d mentions, %d supersedes, %d similar)\n\n",
		mems, notes, len(g.Edges), rel["mentions"], rel["supersedes"], rel["similar"])

	// Hubs: the best-connected nodes are where knowledge concentrates.
	hubs := append([]memory.GraphNode(nil), g.Nodes...)
	sort.Slice(hubs, func(a, b int) bool { return hubs[a].Degree > hubs[b].Degree })
	fmt.Println("most connected")
	shown := 0
	for _, n := range hubs {
		if n.Degree == 0 || shown >= 6 {
			continue
		}
		tag := n.Kind
		if n.Type == "note" {
			tag = "note:" + n.Kind
		}
		fmt.Printf("  %2d links  (%-12s) %s\n", n.Degree, tag, truncateLine(n.Label, 52))
		shown++
	}

	fmt.Println("\nlinks to the vault")
	links := 0
	for _, e := range g.Edges {
		if e.Rel != "mentions" {
			continue
		}
		if links >= 12 {
			fmt.Println("  …")
			break
		}
		fmt.Printf("  %s → %s\n", truncateLine(nodeLabel(g, e.Src), 40), nodeLabel(g, e.Dst))
		links++
	}
	if links == 0 {
		fmt.Println("  (none yet — memories will link to people/project/topic notes as they mention them)")
	}
	fmt.Println("\ntip: --similar adds meaning-based edges · --mermaid emits a diagram · --json for the widget")
	return nil
}

func nodeLabel(g memory.MemGraph, id string) string {
	for _, n := range g.Nodes {
		if n.ID == id {
			return n.Label
		}
	}
	return id
}

func truncateLine(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > n {
		return s[:n-1] + "…"
	}
	return s
}
