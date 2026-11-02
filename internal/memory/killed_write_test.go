package memory

import (
	"crypto/sha256"
	"strings"
	"testing"
)

// A process killed after a memory file or digest reached the disk, and before
// its hash was recorded, left a file the stamp did not claim. The next store
// took logos's own write for a hand edit and refused, and so did every one
// after it. Seen once in the chaos tier's killed memory add (#371).
func TestAWriteKilledBeforeItsStampIsNotTakenForAHandEdit(t *testing.T) {
	for name, file := range map[string]string{"digest": "/fact.md", "own file": ".md"} {
		t.Run(name, func(t *testing.T) {
			db, _ := vaultDB(t)
			// The same text each time: after the first, each store is a
			// reinforcement that rewrites the memory's own file and the
			// digest, both already stamped, each through its hand-edit guard.
			store := func() error {
				_, err := Store(db, nil, "", &Memory{Text: "Deploys go out on Tuesdays", Kind: Fact, Source: "manual"})
				return err
			}
			if err := store(); err != nil {
				t.Fatal(err)
			}
			// The own file is written first and ends in ".md"; "/fact.md"
			// matches only the digest.
			t.Cleanup(func() { vaultWritten = func(string) {} })
			vaultWritten = func(path string) {
				if strings.HasSuffix(path, file) {
					panic("killed")
				}
			}
			func() {
				defer func() { recover() }()
				store()
			}()
			vaultWritten = func(string) {}

			if err := store(); err != nil {
				t.Fatalf("the store after a killed write was refused: %v", err)
			}
		})
	}
}

// A write that keeps failing — a read-only or full vault — calls stampPending
// once per attempt. Appended beside whatever was there, the column grew a hash
// per attempt, and every one of them stayed a claim the file was logos's own.
func TestARepeatedlyFailingWriteHoldsOnlyOnePendingHash(t *testing.T) {
	db, _ := vaultDB(t)
	r, err := Store(db, nil, "", &Memory{Text: "Deploys go out on Tuesdays", Kind: Fact, Source: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	var uid string
	if err := db.QueryRow(`SELECT uid FROM memories WHERE id = ?`, r.ID).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		next := sha256.Sum256([]byte{byte(i)})
		stampPending(db, Fact, next)
		fileStampPending(db, uid, next)
	}
	for table, where := range map[string]string{"digest_stamps": "kind = 'fact'", "memory_file_stamps": "uid = '" + uid + "'"} {
		var sha string
		if err := db.QueryRow(`SELECT sha FROM ` + table + ` WHERE ` + where).Scan(&sha); err != nil {
			t.Fatal(err)
		}
		if n := strings.Count(sha, ",") + 1; n != 2 {
			t.Errorf("%s holds %d hashes after three failed writes, want the confirmed one and the last pending one", table, n)
		}
	}
}
