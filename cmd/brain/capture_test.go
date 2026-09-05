package main

import (
	"database/sql"
	"testing"
	"time"

	"github.com/Coder8124/brain/internal/capture"
	"github.com/Coder8124/brain/internal/event"
	_ "modernc.org/sqlite"
)

// flushCoalescer must surface a write failure rather than swallow it: this is
// the write on the user's own Ctrl+C, and reporting "stopped, session
// flushed" over a write that actually failed would lose the last in-flight
// session with no trace it happened.
func TestFlushCoalescerReportsAWriteFailureInsteadOfSwallowingIt(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	// No capture.InitStore: the events table does not exist, so the write
	// below must fail.

	c := capture.NewCoalescer(60)
	c.Push(event.Event{TS: time.Now().Unix(), Kind: event.Focus, App: "editor", DurS: 1})

	if err := flushCoalescer(db, c); err == nil {
		t.Fatal("flushCoalescer reported success while the write to a missing table must have failed")
	}
}

func TestFlushCoalescerWritesTheInFlightSession(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	if err := capture.InitStore(db); err != nil {
		t.Fatal(err)
	}

	c := capture.NewCoalescer(60)
	c.Push(event.Event{TS: time.Now().Unix(), Kind: event.Focus, App: "editor", DurS: 1})

	if err := flushCoalescer(db, c); err != nil {
		t.Fatalf("flushCoalescer on a healthy store returned an error: %v", err)
	}
	n, err := capture.Count(db)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("expected the flushed session to be counted, got %d events", n)
	}
}

func TestFlushCoalescerWithNothingInFlightTouchesNothing(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	if err := capture.InitStore(db); err != nil {
		t.Fatal(err)
	}

	if err := flushCoalescer(db, capture.NewCoalescer(60)); err != nil {
		t.Fatalf("flushing an empty coalescer must be a no-op, got: %v", err)
	}
}
