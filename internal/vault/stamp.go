package vault

import (
	"crypto/sha256"
	"os"
	"sync"
)

// Stamps remembers the hash of a whole-file record as this process last wrote
// it, so a store about to rewrite that file can tell "nobody has touched this
// since we wrote it" from "the user edited it by hand".
//
// The stores that rewrite a whole file from the cache — loops.md, the dream
// insight queue, the memory review queue — each told the user that deleting a
// line discards the record "on the next `logos index`". Any write before that
// index regenerated the file from the cache and put the deleted line straight
// back, so the edit only held if the user happened to reindex first. Adopting
// the file before every rewrite fixes that, and this is what makes adopting
// cheap: the common case, where the only writer was us, is one file read and a
// hash compare instead of a full pass.
//
// A hash rather than size and mtime, for the reason internal/memory gives: the
// failure a cheaper check admits is silently losing the user's edit, which is
// the bug this exists to prevent.
//
// Process-local. A stamp we do not have means a full pass, so another process
// writing the file costs one reconcile, never a missed edit.
type Stamps struct {
	mu   sync.Mutex
	sums map[any][sha256.Size]byte
}

// Record stamps the file at path as ours. It reads the file back rather than
// hashing what the caller meant to write, so the stamp describes the disk.
//
// A read that fails clears the stamp instead of leaving the old one. The usual
// cause is not an error at all — a store whose last record went away removes
// its file — and a stale stamp is a claim that bytes on disk are ours: restore
// that file from a backup, the claim matches, the reconcile is skipped, and the
// next write overwrites the restore.
func (s *Stamps) Record(key any, path string) {
	raw, err := os.ReadFile(path)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		delete(s.sums, key)
		return
	}
	if s.sums == nil {
		s.sums = map[any][sha256.Size]byte{}
	}
	s.sums[key] = sha256.Sum256(raw)
}

// Adopted stamps bytes the caller has just read and adopted, for a reconcile
// that is not followed by a write.
func (s *Stamps) Adopted(key any, raw []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sums == nil {
		s.sums = map[any][sha256.Size]byte{}
	}
	s.sums[key] = sha256.Sum256(raw)
}

// Ours reports whether raw is exactly what this process last recorded for key.
func (s *Stamps) Ours(key any, raw []byte) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	got, ok := s.sums[key]
	return ok && got == sha256.Sum256(raw)
}

// Forget drops the stamp for key, so the next reconcile does a full pass. A
// store calls it when it is rebound to another vault: the stamp described a
// file in the old one.
func (s *Stamps) Forget(key any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sums, key)
}
