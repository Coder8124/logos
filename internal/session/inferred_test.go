package session

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func autoRecord(t *testing.T, vault string) Checkpoint {
	t.Helper()
	c, err := WriteAuto(vault, Checkpoint{Project: "shop", Agent: "claude-code",
		State: ActivityLogStateFor("lost-1"), Files: []string{"cart.go"},
		TS: time.Now().Add(-time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func reread(t *testing.T, vault, slug string) Checkpoint {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(vault, filepath.FromSlash(slug)+".md"))
	if err != nil {
		t.Fatal(err)
	}
	return ParseCheckpoint(string(raw))
}

// The index is rebuilt from markdown, so whatever an agent paid to read out of
// a transcript has to be in the file — and has to come back as inferred, not
// as the verified and failed an agent states for itself.
func TestWhatWasInferredFromATranscriptComesBackFromTheVaultAsInferred(t *testing.T) {
	vault := t.TempDir()
	c := autoRecord(t, vault)
	want := Inference{By: "claude-code", Verified: []string{"go test ./cart passed (turn 3)"},
		Failed:  []string{"a nil check in checkout did not stop the crash (turn 2)"},
		Decided: []string{"kept the empty cart path, because refunds use it (turn 1)"}}
	if _, err := InferAuto(vault, c.Slug, want); err != nil {
		t.Fatal(err)
	}

	got := reread(t, vault, c.Slug)
	if got.Inferred == nil {
		t.Fatal("the inference did not survive being read back from the vault")
	}
	if got.Inferred.By != want.By || got.Inferred.At == 0 ||
		!reflect.DeepEqual(got.Inferred.Verified, want.Verified) ||
		!reflect.DeepEqual(got.Inferred.Failed, want.Failed) ||
		!reflect.DeepEqual(got.Inferred.Decided, want.Decided) {
		t.Errorf("inference = %+v, want %+v", got.Inferred, want)
	}
	if len(got.Verified)+len(got.Failed)+len(got.Decisions) != 0 {
		t.Errorf("an inference was read back as the agent's own testimony: verified %v, failed %v, decided %v",
			got.Verified, got.Failed, got.Decisions)
	}
	if !got.Auto {
		t.Error("inferring into a record took away its auto marker")
	}
}

// A distillation that kept nothing was still paid for. Recorded as read, resume
// stops offering it; lost, every resume offers the same transcript again.
func TestAnInferenceThatKeptNothingIsStillRecordedAsRead(t *testing.T) {
	vault := t.TempDir()
	c := autoRecord(t, vault)
	if _, err := InferAuto(vault, c.Slug, Inference{By: "cursor"}); err != nil {
		t.Fatal(err)
	}
	if got := reread(t, vault, c.Slug); got.Inferred == nil || !got.Inferred.Empty() {
		t.Errorf("an empty inference read back as %+v", got.Inferred)
	}
}

// The sweep grows a record when its session did more. The turns an inference
// cites are the same turns, and the reading cost the next agent tokens, so
// growing the record must not throw it away.
func TestAGrownAutoRecordKeepsWhatWasInferredFromIt(t *testing.T) {
	vault := t.TempDir()
	c := autoRecord(t, vault)
	if _, err := InferAuto(vault, c.Slug, Inference{By: "cursor", Failed: []string{"retrying the webhook did nothing (turn 2)"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := GrowAuto(vault, c, Checkpoint{Files: []string{"cart.go", "refund.go"}, TS: time.Now().Unix()}); err != nil {
		t.Fatal(err)
	}
	got := reread(t, vault, c.Slug)
	if got.Inferred == nil || len(got.Inferred.Failed) != 1 {
		t.Errorf("growing the record lost its inference: %+v", got.Inferred)
	}
	if !strings.Contains(strings.Join(got.Files, " "), "refund.go") {
		t.Errorf("the record did not grow: %v", got.Files)
	}
}

// A record somebody made their own carries their testimony. An inference
// written into it would sit beside what they stated as if it were theirs.
func TestAnInferenceIsNotWrittenIntoARecordSomebodyMadeTheirOwn(t *testing.T) {
	vault := t.TempDir()
	c := autoRecord(t, vault)
	path := filepath.Join(vault, filepath.FromSlash(c.Slug)+".md")
	raw, _ := os.ReadFile(path)
	owned := strings.Replace(string(raw), "auto: true\n", "", 1)
	if owned == string(raw) {
		t.Fatal("the record has no auto marker to remove")
	}
	if err := os.WriteFile(path, []byte(owned), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := InferAuto(vault, c.Slug, Inference{By: "cursor", Failed: []string{"x (turn 1)"}}); err == nil {
		t.Error("an inference was written into a record an agent owns without complaint")
	}
	if after, _ := os.ReadFile(path); string(after) != owned {
		t.Errorf("the owned record was changed:\n%s", after)
	}
}

// The sweep can grow a record between an agent's harvest and its distil. The
// reading covers the turns it was served; stored against the grown record it
// would claim the rest as read.
func TestAnInferenceIsRefusedWhenTheRecordGrewWhileItWasRead(t *testing.T) {
	vault := t.TempDir()
	c, err := WriteAuto(vault, Checkpoint{Project: "shop", Agent: "cursor",
		State: ActivityLogStateFor("lost-1"), Files: []string{"cart.go"}, Turns: 4})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := GrowAuto(vault, c, Checkpoint{Files: []string{"cart.go", "refund.go"}, Turns: 9}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(vault, filepath.FromSlash(c.Slug)+".md")
	before, _ := os.ReadFile(path)
	if _, err := InferAuto(vault, c.Slug, Inference{By: "cursor", Turns: 4, Failed: []string{"x (turn 2)"}}); err == nil {
		t.Error("a reading of 4 turns was stored against a record of 9")
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Errorf("the refused reading changed the record:\n%s", after)
	}
}
