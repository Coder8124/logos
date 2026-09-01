package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Coder8124/brain/internal/memory"
	_ "modernc.org/sqlite"
)

// sampleConversations are realistic exchanges that each carry one durable fact,
// with a natural later question that should recall it. They span the memory
// kinds: preferences, people, standing context, and plain facts.
var sampleConversations = []memory.PipelineCase{
	{
		Name:     "email-preference",
		Exchange: "user: Can you keep your replies short? I skim everything and long messages just don't get read.\nassistant: Understood — I'll keep it brief from now on.",
		Probe:    "how long should my emails be?",
		Expect:   []string{"short", "brief", "skim"},
	},
	{
		Name:     "cfo-person",
		Exchange: "user: Loop Sarah Chen in on the budget — she's our CFO and she likes to get updates Monday mornings.\nassistant: Will do, I'll include Sarah on the Monday update.",
		Probe:    "who handles our finances and when do they want updates?",
		Expect:   []string{"sarah", "cfo", "monday"},
	},
	{
		Name:     "q4-launch-context",
		Exchange: "user: Everything's oriented around the Q4 launch of the new hardware line right now — that's the priority through December.\nassistant: Got it, Q4 hardware launch is the focus.",
		Probe:    "what's the big thing we're working toward?",
		Expect:   []string{"q4", "launch", "hardware"},
	},
	{
		Name:     "meeting-time-preference",
		Exchange: "user: Please don't schedule anything before 10am — I do focused work in the early morning and hate being interrupted.\nassistant: No meetings before 10am. Noted.",
		Probe:    "when can you book meetings for me?",
		Expect:   []string{"10", "morning", "before"},
	},
	{
		Name:     "vendor-fact",
		Exchange: "user: Our main cloud vendor is Vercel and the account is under the ops@ email, just so you know for any billing stuff.\nassistant: Noted — Vercel, billed to ops@.",
		Probe:    "which cloud provider do we use?",
		Expect:   []string{"vercel"},
	},
	{
		Name:     "colleague-sensitivity",
		Exchange: "user: Heads up, Alex gets stressed about deadlines, so when you draft anything to him keep the tone gentle and give buffer.\nassistant: I'll be gentle and pad the timelines in anything to Alex.",
		Probe:    "how should I communicate with Alex?",
		Expect:   []string{"alex", "gentle", "deadline", "buffer"},
	},
}

// runPipelineBench seeds a throwaway test vault and runs the full
// extract→store→recall loop, reporting correctness and efficiency.
func runPipelineBench() error {
	rt, err := openRouter()
	if err != nil {
		return err
	}

	dir, err := os.MkdirTemp("", "brain-pipeline-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	db, err := sql.Open("sqlite", filepath.Join(dir, "test.db"))
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)
	defer db.Close()

	fmt.Printf("· extract→recall pipeline over %d seeded conversations (fresh test vault)\n\n", len(sampleConversations))
	rep, err := memory.RunPipeline(db, rt, sampleConversations)
	if err != nil {
		return err
	}

	for _, d := range rep.Details {
		fmt.Println("  " + d)
	}
	fmt.Printf("\n─── correctness ───\n")
	fmt.Printf("  extract→recall accuracy: %.0f%% (%d/%d facts survived the round trip)\n",
		rep.Accuracy()*100, rep.RecallHits, rep.Cases)
	fmt.Printf("  memories learned: %d · dedup growth on re-learn: %d (want 0)\n", rep.Learned, rep.Duplicates)
	fmt.Printf("  final store size: %d\n", rep.MemoryCount)

	fmt.Printf("\n─── efficiency ───\n")
	fmt.Printf("  extraction: %s total · %s per conversation\n",
		rep.LearnTime.Round(1e6), (rep.LearnTime / time.Duration(len(sampleConversations))).Round(1e6))
	fmt.Printf("  recall:     %s total · %s per query\n",
		rep.RecallTime.Round(1e6), (rep.RecallTime / time.Duration(len(sampleConversations))).Round(1e6))
	return nil
}
