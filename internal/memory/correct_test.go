package memory

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

func recalled(mems []Memory, text string) bool {
	for _, m := range mems {
		if m.Text == text {
			return true
		}
	}
	return false
}

// A correction is the user saying the old memory was wrong. Recall must stop
// returning it whatever the question shares with it — the old wording is the
// likeliest thing to be asked again — and the history must still say what was
// believed before, including after the index is deleted and rebuilt.
func TestACorrectionReplacesTheOldMemoryInRecallAndKeepsItInHistory(t *testing.T) {
	db, dir := vaultDB(t)
	const was, now = "Staging runs on port 8080", "Staging runs on port 9090 since the proxy moved"
	old := Memory{Text: was, Kind: Fact, Source: "manual", Project: "shop"}
	if _, err := Store(db, nil, "", &old); err != nil {
		t.Fatal(err)
	}

	res, err := Correct(db, nil, "", old.ID, &Memory{Text: now, Source: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	if res.ID == 0 || res.ID == old.ID || res.Replaced != old.ID {
		t.Fatalf("the correction returned %+v, want a new memory replacing #%d", res, old.ID)
	}

	for _, q := range []string{"staging port 8080", "what port does staging run on"} {
		mems, err := Recall(db, nil, "", q, 5)
		if err != nil {
			t.Fatal(err)
		}
		if recalled(mems, was) {
			t.Errorf("recall %q still returned the corrected memory", q)
		}
		if !recalled(mems, now) {
			t.Errorf("recall %q did not return the correction", q)
		}
	}
	all, _ := All(db)
	for _, m := range all {
		if m.ID == res.ID && (m.Kind != Fact || m.Project != "shop") {
			t.Errorf("the correction is %s in %q, want the old memory's fact in shop", m.Kind, m.Project)
		}
	}

	wiped := testDB(t)
	SetVault(wiped, dir)
	t.Cleanup(func() { SetVault(wiped, "") })
	if _, _, err := Import(wiped, nil, "", dir); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportLog(wiped, dir); err != nil {
		t.Fatal(err)
	}
	mems, err := Recall(wiped, nil, "", "staging port 8080", 5)
	if err != nil {
		t.Fatal(err)
	}
	if recalled(mems, was) {
		t.Errorf("the corrected memory came back after the index was rebuilt")
	}
	hist, err := History(wiped, old.ID)
	if err != nil {
		t.Fatal(err)
	}
	var trail []string
	replaced := false
	for _, e := range hist {
		trail = append(trail, e.Event)
		if e.MemID == old.ID && e.Event == EvSuperseded && e.RefID == res.ID {
			replaced = true
		}
	}
	if !replaced {
		t.Errorf("after the rebuild the old memory's history is %s, want it superseded by #%d", strings.Join(trail, ", "), res.ID)
	}
}

// A pinned fact that gets corrected is still the fact the user pinned; and a
// memory already replaced is not the one to correct — the caller is told which
// one is, rather than forking the history into two "current" answers.
func TestACorrectionKeepsThePinAndRefusesAMemoryAlreadyReplaced(t *testing.T) {
	db, _ := vaultDB(t)
	old := Memory{Text: "Deploys go out on Fridays", Kind: Fact, Source: "manual"}
	if _, err := Store(db, nil, "", &old); err != nil {
		t.Fatal(err)
	}
	if err := Pin(db, old.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := Correct(db, nil, "", old.ID, &Memory{Text: "Deploys go out on Fridays", Source: "manual"}); err == nil {
		t.Error("correcting a memory to its own text succeeded")
	}
	res, err := Correct(db, nil, "", old.ID, &Memory{Text: "Deploys go out on Tuesdays", Source: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	all, _ := All(db)
	for _, m := range all {
		if m.ID == res.ID && m.Pin != PinAlways {
			t.Errorf("the correction has pin %d, want the old memory's pin carried over", m.Pin)
		}
	}
	_, err = Correct(db, nil, "", old.ID, &Memory{Text: "Deploys go out on Mondays", Source: "manual"})
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("#%d", res.ID)) {
		t.Errorf("correcting a replaced memory returned %v, want a refusal naming #%d", err, res.ID)
	}
	if _, err := Correct(db, nil, "", 999, &Memory{Text: "anything", Source: "manual"}); err == nil {
		t.Error("correcting a memory that does not exist succeeded")
	}
}

// The fingerprint is unique across the vault, so the corrected text can
// already belong to a memory somewhere else. Folding the correction into that
// one would leave this project with no current version of the fact; in the
// same project and kind it is the right answer, and the pin goes with it.
func TestACorrectionThatMatchesAnotherMemoryOnlyFoldsIntoOneInTheSameProject(t *testing.T) {
	db, _ := vaultDB(t)
	store := func(text, project string, queued bool) *Memory {
		m := &Memory{Text: text, Kind: Fact, Source: "manual", Project: project, Quarantined: queued}
		if _, err := Store(db, nil, "", m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	active := func(id int64) bool {
		all, _ := All(db)
		for _, m := range all {
			if m.ID == id {
				return true
			}
		}
		return false
	}

	old := store("Staging runs on port 8080", "shop", false)
	store("Staging runs on port 9090", "blog", false)
	if _, err := Correct(db, nil, "", old.ID, &Memory{Text: "Staging runs on port 9090", Source: "manual"}); err == nil {
		t.Error("a correction folded into a memory of another project")
	}
	if !active(old.ID) {
		t.Error("a refused correction retired the memory it was correcting")
	}

	queued := store("Staging runs on port 7070", "shop", true)
	if _, err := Correct(db, nil, "", old.ID, &Memory{Text: "Staging runs on port 7070", Source: "manual"}); err == nil {
		t.Error("a correction folded into a proposal still waiting for review")
	}
	hist, _ := History(db, queued.ID)
	for _, e := range hist {
		if e.Event == EvReinforced {
			t.Error("a refused correction reinforced the queued proposal it matched")
		}
	}

	same := store("Staging runs on port 6060", "shop", false)
	if err := Pin(db, old.ID); err != nil {
		t.Fatal(err)
	}
	res, err := Correct(db, nil, "", old.ID, &Memory{Text: "Staging runs on port 6060", Source: "manual"})
	if err != nil || !res.Existing || res.ID != same.ID {
		t.Fatalf("correcting to an existing memory's text returned %+v, %v; want #%d, existing", res, err, same.ID)
	}
	all, _ := All(db)
	for _, m := range all {
		if m.ID == same.ID && m.Pin != PinAlways {
			t.Errorf("the memory the correction folded into has pin %d, want the old memory's pin", m.Pin)
		}
	}
}

// Excluding a memory was a judgement about that wording; the correction is new
// wording, said by someone who wants it believed. Carrying the exclusion would
// hide the correction from every recall while the CLI reports it stored.
func TestACorrectionOfAnExcludedMemoryIsRecalled(t *testing.T) {
	db, _ := vaultDB(t)
	old := Memory{Text: "Staging runs on port 8080", Kind: Fact, Source: "manual"}
	if _, err := Store(db, nil, "", &old); err != nil {
		t.Fatal(err)
	}
	if err := Exclude(db, old.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := Correct(db, nil, "", old.ID, &Memory{Text: "Staging runs on port 9090", Source: "manual"}); err != nil {
		t.Fatal(err)
	}
	mems, err := Recall(db, nil, "", "staging port", 5)
	if err != nil {
		t.Fatal(err)
	}
	if !recalled(mems, "Staging runs on port 9090") {
		t.Error("the correction of an excluded memory is not recalled")
	}

	// The same when the corrected text is a memory the user had excluded.
	hidden := Memory{Text: "Staging runs on port 7070", Kind: Fact, Source: "manual"}
	if _, err := Store(db, nil, "", &hidden); err != nil {
		t.Fatal(err)
	}
	if err := Exclude(db, hidden.ID); err != nil {
		t.Fatal(err)
	}
	all, _ := All(db)
	var current int64
	for _, m := range all {
		if m.Text == "Staging runs on port 9090" {
			current = m.ID
		}
	}
	if res, err := Correct(db, nil, "", current, &Memory{Text: "Staging runs on port 7070", Source: "manual"}); err != nil || !res.Existing {
		t.Fatalf("correcting to an excluded memory's text returned %+v, %v", res, err)
	}
	if mems, _ = Recall(db, nil, "", "staging port", 5); !recalled(mems, "Staging runs on port 7070") {
		t.Error("a correction folded into an excluded memory is not recalled")
	}
}

// The owner of the corrected text is settled before anything is written so a
// refused correction leaves no trace. Another kind's writer can still store the
// same text between that check and the insert; the correction is refused then
// too, and must not be logged as having reinforced the memory it lost to.
func TestACorrectionThatLosesTheRaceForItsTextLeavesNoTrace(t *testing.T) {
	db, _ := vaultDB(t)
	old := Memory{Text: "Staging runs on port 8080", Kind: Fact, Source: "manual"}
	if _, err := Store(db, nil, "", &old); err != nil {
		t.Fatal(err)
	}
	var rival Memory
	correctRace = func(db *sql.DB, text string) {
		rival = Memory{Text: text, Kind: Preference, Source: "manual"}
		if _, err := Store(db, nil, "", &rival); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { correctRace = func(*sql.DB, string) {} })
	if _, err := Correct(db, nil, "", old.ID, &Memory{Text: "Staging runs on port 9090", Source: "manual"}); err == nil {
		t.Fatal("a correction whose text was stored a moment earlier succeeded")
	}
	hist, _ := History(db, rival.ID)
	for _, e := range hist {
		if e.Event == EvReinforced {
			t.Error("the refused correction is logged as reinforcing the memory it lost to")
		}
	}
}
