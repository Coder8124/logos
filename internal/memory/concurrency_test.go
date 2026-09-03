package memory

import (
	"fmt"
	"sync"
	"testing"
)

// Two agents remembering at the same moment is the ordinary case for this
// product, not an edge one: Claude Code and Cursor each run their own MCP
// server against the same vault, and a single agent can issue parallel tool
// calls. So this is the concurrency the store has to survive.
//
// It did not. nextID reads MAX(id) and the insert was a separate statement, so
// concurrent writers all chose the same number; INSERT OR IGNORE swallowed the
// primary key conflict and Store returned OutcomeNoop with a nil error. Eight
// simultaneous remembers left two memories, and every caller was told its write
// had succeeded — the worst available shape for a bug in a memory product.

func TestConcurrentRemembersAllSurvive(t *testing.T) {
	const writers = 8
	// Repeated because the loss depended on scheduling: a single round passed
	// often enough to look healthy.
	for round := range 20 {
		db, _ := vaultDB(t)

		var wg sync.WaitGroup
		for i := range writers {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				m := Memory{
					Text:   fmt.Sprintf("round %d: writer %d recorded a distinct fact", round, i),
					Kind:   Fact,
					Source: "manual",
				}
				if _, err := Store(db, nil, "", &m); err != nil {
					t.Errorf("writer %d: %v", i, err)
				}
			}(i)
		}
		wg.Wait()

		all, err := All(db)
		if err != nil {
			t.Fatal(err)
		}
		if len(all) != writers {
			t.Fatalf("round %d: %d of %d memories survived", round, len(all), writers)
		}
		// Distinct ids, or memory_log and every citation pointing at one are
		// describing something other than the memory they name.
		ids := map[int64]bool{}
		for _, m := range all {
			if ids[m.ID] {
				t.Fatalf("round %d: id %d used twice", round, m.ID)
			}
			ids[m.ID] = true
		}
	}
}

// The receipt is the only thing a caller has to go on, and agents relay it to
// the user verbatim. Under contention it must still say what actually happened.
func TestEveryConcurrentRememberGetsAnHonestReceipt(t *testing.T) {
	db, _ := vaultDB(t)

	const writers = 8
	var mu sync.Mutex
	outcomes := map[string]int{}
	var wg sync.WaitGroup
	for i := range writers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			m := Memory{Text: fmt.Sprintf("writer %d had something to say", i), Kind: Fact, Source: "manual"}
			r, err := Store(db, nil, "", &m)
			if err != nil {
				t.Errorf("writer %d: %v", i, err)
				return
			}
			mu.Lock()
			outcomes[r.Outcome]++
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	if outcomes[OutcomeNoop] != 0 {
		t.Errorf("%d writers were told nothing happened", outcomes[OutcomeNoop])
	}
	if outcomes[EvCreated] != writers {
		t.Errorf("%d creations reported, want %d (outcomes: %v)", outcomes[EvCreated], writers, outcomes)
	}
}

// The same sentence twice is still corroboration, not a new memory — the retry
// must not turn a fingerprint conflict into a fresh row under a new id.
func TestTheSameSentenceConcurrentlyIsStillOneMemory(t *testing.T) {
	db, _ := vaultDB(t)

	const writers = 8
	var wg sync.WaitGroup
	for range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m := Memory{Text: "the billing service runs on Postgres", Kind: Fact, Source: "manual"}
			if _, err := Store(db, nil, "", &m); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()

	all, err := All(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("%d copies of one sentence stored", len(all))
	}
}
