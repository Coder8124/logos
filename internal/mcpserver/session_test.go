package mcpserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// Session state used to live on Server, so these two properties could not even
// be stated: one process meant one project and one response stream. Both tests
// fail against that shape, which is the point of keeping them.

// A daemon serving several clients has one working directory and it belongs to
// none of them. Two sessions must therefore scope from what each client said,
// not from what the process happens to be sitting in.
func TestTwoSessionsOnOneServerScopeIndependently(t *testing.T) {
	srv := &Server{DB: testDB(t), vault: t.TempDir()}
	t.Setenv("BRAIN_PROJECT", "")

	alpha := &Session{Server: srv, roots: []string{"/work/alpha"}}
	beta := &Session{Server: srv, roots: []string{"/work/beta"}}

	if got := alpha.resolveProject(""); got != "alpha" {
		t.Fatalf("alpha resolved to %q", got)
	}
	if got := beta.resolveProject(""); got != "beta" {
		t.Fatalf("beta resolved to %q, so one session re-scoped the other", got)
	}
	// Resolving beta must not have disturbed alpha's already-settled answer.
	if got := alpha.resolveProject(""); got != "alpha" {
		t.Fatalf("alpha became %q after beta resolved", got)
	}
}

// The encoder used to be a field on Server, so two clients answered at once
// would interleave frames onto one stream and corrupt both. Each Serve call
// owns its writer; this asserts every reply lands on the stream that asked.
func TestConcurrentServeCallsDoNotShareAStream(t *testing.T) {
	const clients, perClient = 3, 20
	srv := &Server{DB: testDB(t), vault: t.TempDir()}

	outs := make([]bytes.Buffer, clients)
	var wg sync.WaitGroup
	for c := 0; c < clients; c++ {
		wg.Add(1)
		go func(c int) {
			defer wg.Done()
			var in bytes.Buffer
			for n := 0; n < perClient; n++ {
				fmt.Fprintf(&in, `{"jsonrpc":"2.0","id":%d,"method":"ping"}`+"\n", c*1000+n)
			}
			if err := srv.Serve(&in, &outs[c]); err != nil {
				t.Errorf("client %d: %v", c, err)
			}
		}(c)
	}
	wg.Wait()

	for c := 0; c < clients; c++ {
		lines := strings.Split(strings.TrimSpace(outs[c].String()), "\n")
		if len(lines) != perClient {
			t.Fatalf("client %d got %d replies, want %d", c, len(lines), perClient)
		}
		for n, line := range lines {
			var resp struct {
				ID int `json:"id"`
			}
			if err := json.Unmarshal([]byte(line), &resp); err != nil {
				t.Fatalf("client %d reply %d is not valid JSON (%v): %s", c, n, err, line)
			}
			if want := c*1000 + n; resp.ID != want {
				t.Fatalf("client %d reply %d has id %d, want %d — streams crossed",
					c, n, resp.ID, want)
			}
		}
	}
}
