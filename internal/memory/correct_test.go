package memory

import (
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
