package session

import (
	"os"
	"path/filepath"
	"testing"
)

// The verdicts are the vault's, not the index's, and the file is append-only:
// re-confirming a ruling retired by mistake has to bring it back.
func TestTheLastVerdictOnARulingWins(t *testing.T) {
	vault := t.TempDir()
	ruling := "streaming the export — reader.go loads the whole file"
	if err := Retire(vault, "api", ruling); err != nil {
		t.Fatal(err)
	}
	got, err := Reviews(vault, "api")
	if err != nil {
		t.Fatal(err)
	}
	if !got[ReviewKey(ruling)].Retired {
		t.Fatalf("a retired ruling did not read back as retired: %+v", got)
	}
	if err := Reconfirm(vault, "api", ruling, "abc1234"); err != nil {
		t.Fatal(err)
	}
	got, _ = Reviews(vault, "api")
	if r := got[ReviewKey(ruling)]; r.Retired || r.Commit != "abc1234" {
		t.Fatalf("re-confirming after a retire did not win: %+v", r)
	}
}

// A person is free to annotate the file. One line in a shape this does not
// write must not cost every other verdict, and the file must never be read
// as the newest checkpoint.
func TestAHandWrittenLineInTheReviewFileIsSkipped(t *testing.T) {
	vault := t.TempDir()
	ruling := "mmap the export — needs a seekable source"
	if err := Retire(vault, "api", ruling); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(vault, CheckpointDir, "api", ReviewedFile)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("- not sure about this one\n- 2026-10-24T00:00:00Z shelved: something\n")
	f.Close()

	got, err := Reviews(vault, "api")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[ReviewKey(ruling)].Retired {
		t.Fatalf("hand-written lines changed the verdicts: %+v", got)
	}
	if IsCheckpointFile(ReviewedFile) {
		t.Fatal("the review file would be read as a checkpoint")
	}
}

func TestAMissingReviewFileIsNoVerdicts(t *testing.T) {
	got, err := Reviews(t.TempDir(), "api")
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
}
