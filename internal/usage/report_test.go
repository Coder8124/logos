package usage

import (
	"math"
	"testing"
	"time"
)

func at(day string, hour int) int64 {
	t, err := time.ParseInLocation("2006-01-02", day, time.UTC)
	if err != nil {
		panic(err)
	}
	return t.Add(time.Duration(hour) * time.Hour).Unix()
}

func resume(day, from, reader string, sent int) Event {
	return Event{TS: at(day, 10), Kind: KindPack, Via: "mcp:resume", Project: "shop", From: from, Reader: reader, Sent: sent}
}

func window(t *testing.T, events []Event, stamps []CheckpointStamp, project string) Report {
	t.Helper()
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	return Build(events, stamps, from, to, project, 40)
}

func TestARestoreIsAcrossAgentsWithinOneOrUnattributed(t *testing.T) {
	for _, tc := range []struct {
		name string
		e    Event
		want string
	}{
		{"another agent wrote it", resume("2026-10-01", "claude", "cursor", 100), CrossAgent},
		{"same agent, later session", resume("2026-10-01", "claude", "claude", 100), SameAgent},
		{"reader is a bare shell", resume("2026-10-01", "claude", "cli", 100), Unattributed},
		{"writer is a bare shell", resume("2026-10-01", "cli", "cursor", 100), Unattributed},
		{"writer unknown", resume("2026-10-01", "", "cursor", 100), Unattributed},
		{"not a resume", Event{Kind: KindPack, Via: "mcp:context", From: "claude", Reader: "cursor"}, ""},
		{"not a pack", Event{Kind: KindDeadEnd, Via: "mcp:resume", From: "claude", Reader: "cursor"}, ""},
	} {
		if got := RestoreKind(tc.e); got != tc.want {
			t.Errorf("%s: RestoreKind = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestRestoreKindAgreesWithHandoff(t *testing.T) {
	for _, e := range []Event{
		resume("2026-10-01", "claude", "cursor", 1),
		resume("2026-10-01", "claude", "claude", 1),
		resume("2026-10-01", "cli", "cursor", 1),
		resume("2026-10-01", "claude", "cli", 1),
	} {
		if (RestoreKind(e) == CrossAgent) != Handoff(e) {
			t.Errorf("%+v: the report and `logos usage` disagree on what a handoff is", e)
		}
	}
}

func TestEveryDayGetsARow(t *testing.T) {
	r := window(t, nil, nil, "")
	if len(r.Days) != 5 {
		t.Fatalf("got %d day rows for 1–5 Oct, want 5", len(r.Days))
	}
	if r.Days[0].Date != "2026-10-01" || r.Days[4].Date != "2026-10-05" {
		t.Errorf("window is %s..%s", r.Days[0].Date, r.Days[4].Date)
	}
}

func TestCheckpointsCountedPerDay(t *testing.T) {
	stamps := []CheckpointStamp{
		{TS: at("2026-10-02", 9), Project: "shop", Agent: "claude"},
		{TS: at("2026-10-02", 15), Project: "shop", Agent: "cursor", HandoffTo: "claude"},
		{TS: at("2026-10-02", 18), Project: "shop", Agent: "claude", Auto: true},
		{TS: at("2026-10-04", 9), Project: "shop", Agent: "claude"},
		{TS: at("2026-09-20", 9), Project: "shop", Agent: "claude"}, // before the window
	}
	r := window(t, nil, stamps, "")
	if d := r.Days[1]; d.Checkpoints != 2 || d.Declared != 1 || d.Auto != 1 {
		t.Errorf("2 Oct: %+v, want 2 checkpoints, 1 declared handoff, 1 auto", d)
	}
	if d := r.Days[3]; d.Checkpoints != 1 {
		t.Errorf("4 Oct: %+v, want 1 checkpoint", d)
	}
	if r.Total.Checkpoints != 3 || r.Total.Auto != 1 || r.Total.Declared != 1 {
		t.Errorf("total %+v, want 3 / 1 auto / 1 declared; the 20 Sep one is outside the window", r.Total)
	}
	if len(r.Each) != 4 {
		t.Errorf("Each lists %d checkpoints, want the 4 in the window", len(r.Each))
	}
	for i := 1; i < len(r.Each); i++ {
		if r.Each[i].TS < r.Each[i-1].TS {
			t.Fatal("Each is not oldest first")
		}
	}
}

func TestHandoffsSplitByAgent(t *testing.T) {
	events := []Event{
		resume("2026-10-01", "claude", "cursor", 400),
		resume("2026-10-01", "claude", "cursor", 400),
		resume("2026-10-03", "cursor", "claude", 400),
		resume("2026-10-03", "claude", "claude", 400),
		resume("2026-10-03", "claude", "cli", 400),
	}
	r := window(t, events, nil, "")
	if r.Total.Restores != 5 || r.Total.Cross != 3 || r.Total.Same != 1 || r.Total.Unattributed != 1 {
		t.Fatalf("total %+v, want 5 restores: 3 cross-agent, 1 same-agent, 1 unattributed", r.Total)
	}
	if len(r.Cross) != 2 || r.Cross[0].From != "claude" || r.Cross[0].Reader != "cursor" || r.Cross[0].Restores != 2 {
		t.Errorf("cross-agent pairs %+v, want claude to cursor first with 2", r.Cross)
	}
	if len(r.Same) != 1 || r.Same[0].From != "claude" || r.Same[0].Reader != "claude" {
		t.Errorf("same-agent pairs %+v, want claude to claude", r.Same)
	}
}

func TestTimeSavedIsAStatedEstimateOfTheSizeHandedOver(t *testing.T) {
	// 400 tokens is 1600 characters is 320 words; at 40 words a minute, 8 minutes.
	if got := RetypeMinutes(400, 40); math.Abs(got-8) > 1e-9 {
		t.Fatalf("RetypeMinutes(400, 40) = %v, want 8", got)
	}
	if RetypeMinutes(0, 40) != 0 || RetypeMinutes(400, 0) != 0 || RetypeMinutes(-5, 40) != 0 {
		t.Error("nothing handed over, or no typing speed, must be no time, not a guess")
	}

	events := []Event{
		resume("2026-10-01", "claude", "cursor", 400),
		resume("2026-10-01", "claude", "claude", 200),
		resume("2026-10-01", "claude", "cli", 4000), // unattributed: no minutes claimed
	}
	r := window(t, events, nil, "")
	if math.Abs(r.Total.CrossMinutes-8) > 1e-9 || math.Abs(r.Total.SameMinutes-4) > 1e-9 {
		t.Errorf("minutes: cross %v same %v, want 8 and 4", r.Total.CrossMinutes, r.Total.SameMinutes)
	}
	if r.WPM != 40 {
		t.Errorf("WPM = %d, the report must carry the assumption it used", r.WPM)
	}

	if Build(events, nil, r.From, r.To, "", 0).WPM != DefaultWPM {
		t.Error("no speed given must fall back to the default and say so")
	}
	if slower := Build(events, nil, r.From, r.To, "", 20); slower.Total.CrossMinutes <= r.Total.CrossMinutes {
		t.Error("a slower typist saves more minutes per handoff, not fewer")
	}
}

func TestProjectFilterAppliesToEverything(t *testing.T) {
	events := []Event{
		resume("2026-10-01", "claude", "cursor", 400),
		{TS: at("2026-10-01", 11), Kind: KindPack, Via: "mcp:resume", Project: "other", From: "claude", Reader: "cursor", Sent: 400},
	}
	stamps := []CheckpointStamp{
		{TS: at("2026-10-01", 9), Project: "shop", Agent: "claude"},
		{TS: at("2026-10-01", 9), Project: "other", Agent: "claude"},
	}
	r := window(t, events, stamps, "shop")
	if r.Total.Checkpoints != 1 || r.Total.Cross != 1 || len(r.Each) != 1 {
		t.Errorf("filtered to shop: %+v", r.Total)
	}
	if all := window(t, events, stamps, ""); all.Total.Checkpoints != 2 || all.Total.Cross != 2 {
		t.Errorf("unfiltered: %+v", all.Total)
	}
}

func TestDoctorTestingItselfIsNotAHandoff(t *testing.T) {
	probe := Event{TS: at("2026-10-02", 10), Kind: KindPack, Via: "mcp:resume", Project: "logos-selftest-4242", From: "logos-doctor", Reader: "logos-doctor", Sent: 228}
	r := window(t, []Event{probe, resume("2026-10-02", "claude", "claude", 400)}, nil, "")
	if r.Total.Restores != 1 || r.Total.Same != 1 || r.SelfTests != 1 {
		t.Errorf("restores %d, within one agent %d, self-tests %d; want 1, 1, 1", r.Total.Restores, r.Total.Same, r.SelfTests)
	}
	if math.Abs(r.Total.SameMinutes-8) > 1e-9 {
		t.Errorf("the probe's 228 tokens were priced as time saved: %v minutes", r.Total.SameMinutes)
	}
}
