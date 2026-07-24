package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/pragun/brain/internal/memory"
	"github.com/pragun/brain/internal/router"
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
			// salience = how much it matters; conf = how sure we are it's true.
			fmt.Printf("  [%d] (%-10s sal %.2f · conf %s) %s%s\n",
				m.ID, m.Kind, m.Salience, confBar(m.Confidence), m.Text, meta)
		}
		return nil
	}

	switch args[0] {
	case "log":
		return memoryLog(ix.DB, flagInt(args, "--n", 40))
	case "history":
		id := parseID(args)
		if id == 0 {
			return fmt.Errorf("usage: brain memory history <id>")
		}
		return memoryHistory(ix.DB, id)
	case "graph":
		return memoryGraph(ix.DB, args)
	case "add":
		text := strings.TrimSpace(strings.Join(args[1:], " "))
		if text == "" {
			return fmt.Errorf("usage: brain memory add <fact>")
		}
		rt, err := openRouter()
		if err != nil {
			return err
		}
		embed, _ := rt.Model(router.T0)
		_, err = memory.Store(ix.DB, rt.Local(), embed, &memory.Memory{
			Text: text, Kind: memory.Fact, Salience: 0.7, Source: "manual", Created: time.Now().Unix(),
		})
		if err != nil {
			return err
		}
		fmt.Println("remembered.")
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
		id := parseID(args)
		if id == 0 {
			return fmt.Errorf("usage: brain memory forget <id>")
		}
		if err := memory.Forget(ix.DB, id); err != nil {
			return err
		}
		fmt.Println("forgotten.")
	default:
		return fmt.Errorf("usage: brain memory [add <fact> | forget <id> | log | history <id> | consolidate]")
	}
	return nil
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

// memoryLog prints the timeline — git history for memory, newest first.
func memoryLog(db *sql.DB, n int) error {
	entries, err := memory.Timeline(db, n)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		fmt.Println("no memory history yet — it fills in as the assistant learns, corroborates, and revises.")
		return nil
	}
	fmt.Printf("memory timeline · %d most recent events\n\n", len(entries))
	for _, e := range entries {
		when := time.Unix(e.TS, 0).Format("Jan 02 15:04")
		ref := ""
		if e.RefID != 0 {
			ref = fmt.Sprintf(" (→ #%d)", e.RefID)
		}
		fmt.Printf("  %s  %-11s #%-4d %s%s\n", when, e.Event, e.MemID, truncateLine(e.Detail, 60), ref)
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
