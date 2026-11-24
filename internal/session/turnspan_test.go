package session

import "testing"

func TestAnAutoRecordIsNotAPredecessorOfAnAgentCheckpointsSpan(t *testing.T) {
	const host = "5e55e55e-0000-4000-8000-00000000abcd"
	first := Checkpoint{Slug: "a", HostSession: host, Turns: 10}
	// A sweep read to turn 50; the agent wrote nothing there.
	auto := Checkpoint{Slug: "b", HostSession: host, Turns: 50, Auto: true}
	last := Checkpoint{Slug: "c", HostSession: host, Turns: 60}

	from, to, ok := TurnSpan(last, []Checkpoint{first, auto, last})
	if !ok || from != 11 || to != 60 {
		t.Fatalf("span = %d-%d (ok %v), want 11-60", from, to, ok)
	}
}
