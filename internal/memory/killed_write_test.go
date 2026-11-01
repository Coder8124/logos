package memory

import (
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
