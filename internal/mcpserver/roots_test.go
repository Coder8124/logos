package mcpserver

import (
	"strings"
	"testing"
	"time"
)

// initializeWithRoots is startAsync's handshake from a host that declares the
// roots capability, as Cursor does: it sends no roots up front and expects to be
// asked for them.
func initializeWithRoots(t *testing.T, c *asyncClient) {
	t.Helper()
	c.send(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{"roots":{"listChanged":true}},"clientInfo":{"name":"cursor","version":"1"}}}`)
	if _, ok := c.await(t, `"id":1`, 3*time.Second); !ok {
		t.Fatal("no response to initialize")
	}
	c.send(t, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
}

// answerRoots waits for the server's roots/list request and answers it with dir.
func answerRoots(t *testing.T, c *asyncClient, dir string) {
	t.Helper()
	line, ok := c.await(t, `"roots/list"`, 3*time.Second)
	if !ok {
		t.Fatal("the server never asked the host which folder is open")
	}
	id := rawID(line)
	c.send(t, `{"jsonrpc":"2.0","id":`+string(id)+`,"result":{"roots":[{"uri":"file://`+dir+`","name":"open"}]}}`)
}

// Cursor, Cline and Claude Desktop launch the server in / or their own folder,
// so without asking for roots every project-scoped call landed in the wrong
// project or none.
func TestTheServerAsksAHostThatHasRootsWhichFolderIsOpen(t *testing.T) {
	t.Setenv("LOGOS_PROJECT", "")
	c, _ := startBare(t)
	initializeWithRoots(t, c)
	answerRoots(t, c, "/work/code/kestrel-roots")

	line, ok := call(t, c, 2, "remember", map[string]any{"text": "the frame is carbon"})
	if !ok {
		t.Fatal("remember did not answer")
	}
	if !strings.Contains(line, "kestrel-roots") {
		t.Errorf("remember was not scoped to the host's open folder:\n%s", line)
	}
}

// A tool call the host sends before it has answered roots/list is scoped by
// the answer, not by the folder the server happened to start in.
func TestACallSentBeforeTheRootsAnswerWaitsForIt(t *testing.T) {
	t.Setenv("LOGOS_PROJECT", "")
	c, _ := startBare(t)
	initializeWithRoots(t, c)
	line, ok := c.await(t, `"roots/list"`, 3*time.Second)
	if !ok {
		t.Fatal("the server never asked for roots")
	}
	c.send(t, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"remember","arguments":{"text":"the frame is carbon"}}}`)
	time.Sleep(50 * time.Millisecond)
	c.send(t, `{"jsonrpc":"2.0","id":`+string(rawID(line))+`,"result":{"roots":[{"uri":"file:///work/late-roots"}]}}`)

	got, ok := c.await(t, `"id":2`, 5*time.Second)
	if !ok {
		t.Fatal("remember did not answer")
	}
	if !strings.Contains(got, "late-roots") {
		t.Errorf("a call queued behind the roots request was scoped without it:\n%s", got)
	}
}

// The host's answer to roots/list is a response, not a request; treating it as
// one sent the host a "method not found" error for a message it never asked.
func TestTheHostsRootsAnswerIsNotRepliedTo(t *testing.T) {
	c, _ := startBare(t)
	initializeWithRoots(t, c)
	answerRoots(t, c, "/work/kestrel")
	c.send(t, `{"jsonrpc":"2.0","id":9,"method":"tools/list"}`)

	if got := c.linesUntil(t, `"id":9`, 5*time.Second); mentions(got, "method not found") {
		t.Errorf("the server answered the host's response:\n%s", strings.Join(got, "\n"))
	}
}

// A host without the capability would answer roots/list with an error at best,
// so it is never sent.
func TestAHostWithoutRootsIsNotAskedForThem(t *testing.T) {
	c, _ := startAsync(t)
	c.send(t, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	c.send(t, `{"jsonrpc":"2.0","id":9,"method":"tools/list"}`)

	if got := c.linesUntil(t, `"id":9`, 5*time.Second); mentions(got, "roots/list") {
		t.Errorf("a host without roots was asked for them:\n%s", strings.Join(got, "\n"))
	}
}

// A host that declares roots and never answers must not hang every later call.
func TestAHostThatNeverAnswersRootsIsNotWaitedOnForever(t *testing.T) {
	old := rootsTimeout
	rootsTimeout = 200 * time.Millisecond
	t.Cleanup(func() { rootsTimeout = old })

	c, _ := startBare(t)
	initializeWithRoots(t, c)
	if _, ok := call(t, c, 2, "remember", map[string]any{"text": "the frame is carbon"}); !ok {
		t.Fatal("a host that never answered roots/list blocked the next call")
	}
}

// When the root changes — the user opened another folder — later calls follow it.
func TestChangedRootsRescopeLaterCalls(t *testing.T) {
	t.Setenv("LOGOS_PROJECT", "")
	c, _ := startBare(t)
	initializeWithRoots(t, c)
	answerRoots(t, c, "/work/first-folder")
	call(t, c, 2, "remember", map[string]any{"text": "the frame is carbon"})

	c.send(t, `{"jsonrpc":"2.0","method":"notifications/roots/list_changed"}`)
	answerRoots(t, c, "/work/second-folder")
	line, _ := call(t, c, 3, "remember", map[string]any{"text": "the fork is steel"})
	if !strings.Contains(line, "second-folder") {
		t.Errorf("a call after the roots changed kept the old project:\n%s", line)
	}
}

// Started in / by a host that sent no roots, context came back "No project
// matched" with an empty pack, and the agent took that as there being nothing
// recorded rather than as not knowing where it was.
func TestContextWithNoProjectInScopeSaysToPassOne(t *testing.T) {
	t.Setenv("LOGOS_PROJECT", "")
	t.Setenv("LOGOS_WORKTREE", "")
	t.Chdir("/")
	c, _ := startAsync(t)

	line, ok := call(t, c, 2, "context", map[string]any{"task": "add rate limiting to the login endpoint"})
	if !ok {
		t.Fatal("context did not answer")
	}
	if !strings.Contains(line, "pass `project`") {
		t.Errorf("context with no project in scope did not say to pass one:\n%s", line)
	}

	line, _ = call(t, c, 3, "context", map[string]any{"task": "add rate limiting", "project": "app"})
	if strings.Contains(line, "pass `project`") {
		t.Errorf("context told a caller that passed a project to pass one:\n%s", line)
	}
}

// Only a frame carrying a result or an error is a response. A request that is
// merely missing its method is still the host waiting on an answer.
func TestARequestWithNoMethodIsStillAnswered(t *testing.T) {
	c, _ := startAsync(t)
	c.send(t, `{"jsonrpc":"2.0","id":7}`)
	if _, ok := c.await(t, `"id":7`, 3*time.Second); !ok {
		t.Error("a request with no method was left unanswered")
	}
}
