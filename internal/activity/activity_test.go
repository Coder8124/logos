package activity

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAppendAndReadRoundTrip(t *testing.T) {
	v := t.TempDir()
	if err := Append(v, Event{Kind: KindPrompt, Project: "kestrel", Summary: "fix the retry budget"}); err != nil {
		t.Fatal(err)
	}
	got, err := Read(v, Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Summary != "fix the retry budget" {
		t.Fatalf("round trip lost the event: %+v", got)
	}
	if got[0].TS == 0 {
		t.Error("an event with no timestamp should be stamped on the way in")
	}
}

func TestEventsAreOneJSONLineEach(t *testing.T) {
	v := t.TempDir()
	// A summary that would break the file if newlines survived into it.
	Append(v, Event{Kind: KindTool, Summary: "line one\nline two"})
	Append(v, Event{Kind: KindTool, Summary: "second event"})

	files, _ := filepath.Glob(filepath.Join(v, Dir, "*.jsonl"))
	if len(files) != 1 {
		t.Fatalf("want one month file, got %v", files)
	}
	raw, _ := os.ReadFile(files[0])
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines for 2 events, got %d:\n%s", len(lines), raw)
	}
	for i, l := range lines {
		var e Event
		if err := json.Unmarshal([]byte(l), &e); err != nil {
			t.Errorf("line %d is not valid JSON on its own — jq would choke: %v", i, err)
		}
	}
}

// The claim on the box is "pipe it to jq". Two agents in two terminals must not
// be able to produce a half-written line between them.
func TestConcurrentAppendsNeverTearALine(t *testing.T) {
	v := t.TempDir()
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			Append(v, Event{Kind: KindTool, Tool: "Edit", Summary: strings.Repeat("x", 200)})
		}(i)
	}
	wg.Wait()

	files, _ := filepath.Glob(filepath.Join(v, Dir, "*.jsonl"))
	raw, _ := os.ReadFile(files[0])
	for i, l := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		var e Event
		if err := json.Unmarshal([]byte(l), &e); err != nil {
			t.Fatalf("line %d torn: %v", i, err)
		}
	}
	got, _ := Read(v, Query{})
	if len(got) != 40 {
		t.Errorf("want 40 events, got %d", len(got))
	}
}

func TestReadIsNewestFirstAndLimitKeepsTheNewest(t *testing.T) {
	v := t.TempDir()
	now := time.Now().Unix()
	for i := 0; i < 5; i++ {
		Append(v, Event{Kind: KindTool, TS: now + int64(i), Summary: string(rune('a' + i))})
	}
	got, err := Read(v, Query{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2, got %d", len(got))
	}
	if got[0].Summary != "e" || got[1].Summary != "d" {
		t.Errorf("limit kept the oldest instead of the newest: %+v", got)
	}
}

func TestFiltersNarrow(t *testing.T) {
	v := t.TempDir()
	Append(v, Event{Kind: KindTool, Project: "kestrel", Tool: "Edit"})
	Append(v, Event{Kind: KindTool, Project: "harrier", Tool: "Bash"})
	Append(v, Event{Kind: KindPrompt, Project: "kestrel"})

	for _, tc := range []struct {
		name string
		q    Query
		want int
	}{
		{"project", Query{Project: "kestrel"}, 2},
		{"kind", Query{Kind: KindTool}, 2},
		{"tool", Query{Tool: "edit"}, 1}, // case-insensitive
		{"combined", Query{Project: "kestrel", Kind: KindTool}, 1},
		{"no match", Query{Project: "nope"}, 0},
	} {
		got, err := Read(v, tc.q)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != tc.want {
			t.Errorf("%s: want %d, got %d", tc.name, tc.want, len(got))
		}
	}
}

// One bad line must cost one line, not the rest of the month.
func TestAMalformedLineDoesNotEndTheFile(t *testing.T) {
	v := t.TempDir()
	Append(v, Event{Kind: KindTool, Summary: "before"})
	files, _ := filepath.Glob(filepath.Join(v, Dir, "*.jsonl"))
	f, _ := os.OpenFile(files[0], os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString("{this is not json\n")
	f.Close()
	Append(v, Event{Kind: KindTool, Summary: "after"})

	got, err := Read(v, Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want both good events, got %d: %+v", len(got), got)
	}
}

func TestAnEventNeedsAKind(t *testing.T) {
	if err := Append(t.TempDir(), Event{Summary: "orphan"}); err == nil {
		t.Error("a kindless event should be refused, not written")
	}
}

func TestMonthlyFiles(t *testing.T) {
	v := t.TempDir()
	jan := time.Date(2026, 1, 15, 0, 0, 0, 0, time.Local).Unix()
	feb := time.Date(2026, 2, 15, 0, 0, 0, 0, time.Local).Unix()
	Append(v, Event{Kind: KindTool, TS: jan})
	Append(v, Event{Kind: KindTool, TS: feb})

	for _, want := range []string{"2026-01.jsonl", "2026-02.jsonl"} {
		if _, err := os.Stat(filepath.Join(v, Dir, want)); err != nil {
			t.Errorf("want a file named %s a person could guess: %v", want, err)
		}
	}
	got, _ := Read(v, Query{})
	if len(got) != 2 {
		t.Errorf("reading must span months: got %d", len(got))
	}
}

func TestProjectsAreBusiestFirst(t *testing.T) {
	v := t.TempDir()
	for i := 0; i < 3; i++ {
		Append(v, Event{Kind: KindTool, Project: "busy"})
	}
	Append(v, Event{Kind: KindTool, Project: "quiet"})
	got, err := Projects(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "busy" {
		t.Fatalf("want busy first, got %v", got)
	}
}
