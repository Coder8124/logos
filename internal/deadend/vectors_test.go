package deadend

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Coder8124/logos/internal/provider"
	"github.com/Coder8124/logos/internal/session"
)

// embedCounter is a runtime that records how many texts each embeddings
// request carried. down makes every request fail.
type embedCounter struct {
	mu       sync.Mutex
	requests []int
	down     bool
}

func (c *embedCounter) provider(t *testing.T) *provider.Provider {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input []string `json:"input"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		c.mu.Lock()
		c.requests = append(c.requests, len(req.Input))
		down := c.down
		c.mu.Unlock()
		if down {
			http.Error(w, "connection reset by peer", http.StatusBadRequest)
			return
		}
		data := make([]map[string]any, len(req.Input))
		for i, s := range req.Input {
			data[i] = map[string]any{"embedding": []float32{float32(len(s)), 1, 0}}
		}
		json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	t.Cleanup(srv.Close)
	return provider.New("Fake", srv.URL, "")
}

func (c *embedCounter) texts() (total, largest int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, n := range c.requests {
		total += n
		largest = max(largest, n)
	}
	return total, largest
}

// Ruling text only changes when a checkpoint lands, yet every call re-embedded
// the whole corpus: 6.3 s at 1000 rulings on a fast GPU.
func TestASecondCheckEmbedsOnlyTheProposal(t *testing.T) {
	dir, db := seed(t)
	c := &embedCounter{}
	p := c.provider(t)

	if _, err := Check(dir, db, p, "m", "switch to a plastic frame", "kestrel-one", 5); err != nil {
		t.Fatal(err)
	}
	before, _ := c.texts()
	if _, err := Check(dir, db, p, "m", "use a carbon frame", "kestrel-one", 5); err != nil {
		t.Fatal(err)
	}
	after, _ := c.texts()

	if after-before != 1 {
		t.Errorf("the second check embedded %d texts, want only the proposal", after-before)
	}
}

// One request with every ruling in it failed intermittently at 900 inputs.
func TestALargeCorpusIsEmbeddedInBatches(t *testing.T) {
	dir, db := seed(t)
	var failed []string
	for i := range 600 {
		failed = append(failed, fmt.Sprintf("approach %d abandoned — reason %d", i, i))
	}
	if err := session.Commit(db, dir, &session.Checkpoint{
		Project: "big", Agent: "claude", Task: "work", Failed: failed, Next: "carry on",
	}); err != nil {
		t.Fatal(err)
	}
	c := &embedCounter{}

	if _, err := Check(dir, db, c.provider(t), "m", "switch to a plastic frame", "", 5); err != nil {
		t.Fatal(err)
	}
	if _, largest := c.texts(); largest > embedBatch {
		t.Errorf("one embeddings request carried %d texts, want at most %d", largest, embedBatch)
	}
}

// A failed embedding turned every semantic score into 0 and the result read
// exactly like a full check that found nothing.
func TestAFailedEmbeddingSaysTheSemanticCheckWasSkipped(t *testing.T) {
	dir, db := seed(t)
	c := &embedCounter{down: true}

	hits, semantic, err := CheckNoting(dir, db, c.provider(t), "m", "switch to a plastic frame", "kestrel-one", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Error("the lexical match was lost along with the semantic check")
	}
	if note := SemanticSkipped(semantic); !strings.Contains(note, "semantic check skipped") {
		t.Errorf("a failed embedding was not reported: %q", note)
	}
}
