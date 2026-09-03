package index

import (
	"sync"
	"testing"

	_ "modernc.org/sqlite"
)

// Several processes opening one vault at the same moment is the normal first
// run: a coding agent's MCP server, the editor's, and a command typed in a
// terminal. Converting a fresh database to WAL takes an exclusive lock that
// busy_timeout does not wait for, so all but one used to get SQLITE_BUSY —
// and Open turned that into a hard failure, which the user read as "the tool
// is broken" rather than "try again".
func TestManyOpensOnAColdVaultAllSucceed(t *testing.T) {
	v := t.TempDir()

	const n = 12
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ix, err := Open(v)
			if err != nil {
				errs <- err
				return
			}
			defer ix.Close()
			errs <- nil
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("a concurrent open of a cold vault failed: %v", err)
		}
	}
}
