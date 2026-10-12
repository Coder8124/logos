package mcpserver

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/memory"
)

// A recall returns each memory's whole text, and an agent that only wanted to
// see what is there paid for every word of it. Compact mode is the index — an
// id and a line per memory — and the ids are what fetches the rest, so the
// agent pays for the full text only of the memories it chose.
func TestCompactRecallReturnsIdsThatGetExpands(t *testing.T) {
	db := testDB(t)
	sess := &Session{Server: &Server{DB: db, vault: t.TempDir()}}
	long := "the checkout service retries a declined card twice because the payment provider returns a transient decline on the first attempt for 3DS cards, and the retry window is 400ms"
	a := seedMemory(t, db, long, "shop")
	b := seedMemory(t, db, "the checkout tests need STRIPE_TEST=1", "shop")

	out, err := sess.dispatch("recall", map[string]any{"query": "checkout", "project": "shop", "compact": true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "400ms") {
		t.Errorf("compact recall returned the whole text:\n%s", out)
	}
	ids := regexp.MustCompile(`#(\d+)`).FindAllStringSubmatch(out, -1)
	if len(ids) < 2 || !strings.Contains(out, "ids") {
		t.Fatalf("compact recall should list an id per memory and say how to expand them:\n%s", out)
	}

	full, err := sess.dispatch("recall", map[string]any{"ids": fmt.Sprintf("%d, %d", a.ID, b.ID)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(full, long) || !strings.Contains(full, "STRIPE_TEST=1") {
		t.Errorf("recall by id did not return the full text:\n%s", full)
	}
}

// An id is a number an agent copied from an earlier answer, and the memory may
// have changed since. Each way it can be gone is said, never served as if it
// were current or dropped without a word.
func TestRecallByIdSaysWhyAMemoryIsNotServed(t *testing.T) {
	db := testDB(t)
	sess := &Session{Server: &Server{DB: db, vault: t.TempDir()}}
	excluded := seedMemory(t, db, "the old deploy key lives in 1Password", "shop")
	if err := memory.Exclude(db, excluded.ID); err != nil {
		t.Fatal(err)
	}

	out, err := sess.dispatch("recall", map[string]any{"ids": fmt.Sprintf("%d,9999,abc", excluded.ID)})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "1Password") {
		t.Errorf("an excluded memory was served by id:\n%s", out)
	}
	for _, want := range []string{fmt.Sprintf("#%d", excluded.ID), "excluded", "#9999", "No memory", "abc"} {
		if !strings.Contains(out, want) {
			t.Errorf("recall by id does not say %q:\n%s", want, out)
		}
	}
}

// recall no longer requires a query, so an agent that sent neither reads the
// error to learn what to send: one that names only the query hides the ids
// it could have passed instead.
func TestRecallWithNeitherAQueryNorIdsNamesBoth(t *testing.T) {
	sess := &Session{Server: &Server{DB: testDB(t), vault: t.TempDir()}}
	_, err := sess.dispatch("recall", map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "ids") {
		t.Errorf("recall with neither a query nor ids: %v", err)
	}
}

// The answer lines up with the ids the agent asked for. One that is not a
// number was listed first, ahead of the ids before it.
func TestRecallByIdsAnswersInTheOrderAsked(t *testing.T) {
	db := testDB(t)
	sess := &Session{Server: &Server{DB: db, vault: t.TempDir()}}
	a := seedMemory(t, db, "the checkout tests need STRIPE_TEST=1", "shop")

	out, err := sess.dispatch("recall", map[string]any{"ids": fmt.Sprintf("%d,abc,9999", a.ID)})
	if err != nil {
		t.Fatal(err)
	}
	first, bad, missing := strings.Index(out, "STRIPE_TEST"), strings.Index(out, "abc"), strings.Index(out, "#9999")
	if first < 0 || bad < 0 || missing < 0 || !(first < bad && bad < missing) {
		t.Errorf("recall by ids is not in the order asked:\n%s", out)
	}
}
