package memory

import (
	"testing"

	"github.com/Coder8124/logos/internal/gitstate"
)

// The whole point of a content-derived id: two machines that each clone the
// same repository and store the same fact end up agreeing on its id without
// ever talking to each other — which an autoincrement id could never do.
// NormalizeRemote is what makes an ssh clone and an https clone of the same
// repository count as "the same machine" for this purpose.
func TestTwoMachinesWritingTheSameMemoryGetTheSameUid(t *testing.T) {
	sshRemote := gitstate.NormalizeRemote("git@github.com:Coder8124/logos.git")
	httpsRemote := gitstate.NormalizeRemote("https://github.com/Coder8124/logos")
	if sshRemote != httpsRemote {
		t.Fatalf("NormalizeRemote(ssh) = %q, NormalizeRemote(https) = %q, want equal", sshRemote, httpsRemote)
	}

	machineA, _ := vaultDB(t)
	machineB, _ := vaultDB(t)

	mA := Memory{Text: "The CI pipeline deploys from main on green", Kind: Fact, Source: "manual", Remote: sshRemote}
	mB := Memory{Text: "The CI pipeline deploys from main on green", Kind: Fact, Source: "manual", Remote: httpsRemote}
	if _, err := Store(machineA, nil, "", &mA); err != nil {
		t.Fatal(err)
	}
	if _, err := Store(machineB, nil, "", &mB); err != nil {
		t.Fatal(err)
	}
	if mA.Uid == "" || mB.Uid == "" {
		t.Fatalf("expected both memories to get a uid, got %q and %q", mA.Uid, mB.Uid)
	}
	if mA.Uid != mB.Uid {
		t.Fatalf("uid = %q on one machine, %q on the other, for the same fact in the same repository", mA.Uid, mB.Uid)
	}

	// A different repository, the same fact: must not collide.
	machineC, _ := vaultDB(t)
	otherRemote := gitstate.NormalizeRemote("https://github.com/someone-else/unrelated.git")
	mC := Memory{Text: "The CI pipeline deploys from main on green", Kind: Fact, Source: "manual", Remote: otherRemote}
	if _, err := Store(machineC, nil, "", &mC); err != nil {
		t.Fatal(err)
	}
	if mC.Uid == mA.Uid {
		t.Fatalf("the same fact text in a different repository got the same uid %q; remote must be part of the hash", mC.Uid)
	}
}

// Store computes a memory's uid once, from its text, kind and originating
// repository — never again. A hand edit to the vault line (text, confidence
// or salience) must come back through upsert, the reconcile path a hand edit
// actually takes, without disturbing the uid: it is the same fact, however
// the person chose to restate its confidence.
func TestAHandEditedMemoryKeepsItsUid(t *testing.T) {
	db, _ := vaultDB(t)
	m := Memory{Text: "I prefer written proposals over meetings", Kind: Preference, Source: "manual"}
	if _, err := Store(db, nil, "", &m); err != nil {
		t.Fatal(err)
	}

	before, err := ByIDs(db, []int64{m.ID})
	if err != nil || len(before) != 1 || !before[0].Found {
		t.Fatalf("could not fetch the stored memory: err=%v out=%+v", err, before)
	}
	original := before[0].Memory.Uid
	if original == "" {
		t.Fatalf("a stored memory has no uid")
	}

	// What upsert sees when `logos index` reconciles a line a person edited by
	// hand: the same id, different text, confidence and salience.
	edited := before[0].Memory
	edited.Text = "I prefer written proposals over meetings, strongly"
	edited.Confidence = 0.4
	edited.Salience = 0.9
	if _, _, err := upsert(db, nil, "", edited); err != nil {
		t.Fatal(err)
	}

	after, err := ByIDs(db, []int64{m.ID})
	if err != nil || len(after) != 1 || !after[0].Found {
		t.Fatalf("could not refetch after the hand edit: err=%v out=%+v", err, after)
	}
	if after[0].Memory.Text != edited.Text {
		t.Fatalf("the hand edit to text did not take: got %q", after[0].Memory.Text)
	}
	if after[0].Memory.Uid != original {
		t.Fatalf("uid changed from %q to %q after a hand edit to text/confidence/salience", original, after[0].Memory.Uid)
	}
}
