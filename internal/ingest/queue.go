package ingest

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Coder8124/brain/internal/vault"
)

// The queue is the set of candidate notes on disk under <vault>/ingest/. There
// is no queue table: a rebuilt index must not lose a pending candidate
// (invariant 1, which the review queue has already broken once), so "what is
// pending" and "which sessions have been read" are both answered by walking the
// markdown.

// Result reports what a queue write did, so the caller can announce it with a
// number (invariant 3).
type Result struct {
	Path    string // vault-relative path written
	Written bool   // false when an identical candidate was already present
	Reason  string // when !Written: why it was skipped
}

// Put writes a candidate to the vault, atomically and privately. Vault first;
// the caller reindexes after (invariant 2).
//
//   - An unchanged transcript (same hash) that is already queued is skipped, so
//     re-running ingest is idempotent — the file's existence is the cursor.
//   - An edited transcript (same session id, different hash) is written to a
//     hash-suffixed sibling rather than overwriting: the earlier candidate may
//     already be under review, and silently swapping its contents is exactly the
//     kind of durable-state surprise this codebase keeps being bitten by.
func Put(vaultDir string, c Candidate) (Result, error) {
	rel := c.RelPath()
	abs := filepath.Join(vaultDir, filepath.FromSlash(rel))

	if existing, ok := readCandidate(abs); ok {
		if existing.Hash == c.Hash {
			return Result{Path: rel, Written: false, Reason: "already ingested"}, nil
		}
		// Same session id, changed transcript. Land beside it.
		rel = c.variantRelPath()
		abs = filepath.Join(vaultDir, filepath.FromSlash(rel))
		if existing2, ok := readCandidate(abs); ok && existing2.Hash == c.Hash {
			return Result{Path: rel, Written: false, Reason: "already ingested"}, nil
		}
	}

	if err := vault.WriteAtomic(abs, []byte(c.Markdown())); err != nil {
		return Result{}, err
	}
	return Result{Path: rel, Written: true}, nil
}

// variantRelPath is the hash-suffixed path used when a session's transcript
// changed after an earlier candidate was already written.
func (c Candidate) variantRelPath() string {
	h := c.Hash
	if len(h) > 8 {
		h = h[:8]
	}
	name := strings.TrimSuffix(c.Filename(), ".md") + "-" + safeSegment(h) + ".md"
	return filepath.ToSlash(filepath.Join(Dir, c.scope(), name))
}

func readCandidate(abs string) (Candidate, bool) {
	raw, err := os.ReadFile(abs)
	if err != nil {
		return Candidate{}, false
	}
	c := Parse(string(raw))
	if c.SessionID == "" {
		return Candidate{}, false
	}
	return c, true
}

// Pending lists every candidate note in the vault whose status is still
// pending, newest harvest first. A promoted or rejected candidate is kept on
// disk (the record of what was decided) but never offered again.
func Pending(vaultDir string) ([]Candidate, error) {
	all, err := All(vaultDir)
	if err != nil {
		return nil, err
	}
	out := all[:0]
	for _, c := range all {
		if c.Status == StatusPending {
			out = append(out, c)
		}
	}
	return out, nil
}

// All lists every candidate note regardless of status, newest first.
func All(vaultDir string) ([]Candidate, error) {
	root := filepath.Join(vaultDir, Dir)
	var out []Candidate
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // an unreadable subdirectory is skipped, not fatal
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		if c, ok := readCandidate(path); ok {
			out = append(out, c)
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Harvested > out[j].Harvested })
	return out, nil
}

// Find returns the pending candidate whose session id (or filename stem) matches
// ref. Used by `brain ingest review --promote <ref>`.
func Find(vaultDir, ref string) (Candidate, string, bool, error) {
	root := filepath.Join(vaultDir, Dir)
	var (
		found  Candidate
		abs    string
		ok     bool
		walkTS int64
	)
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, werr error) error {
		if werr != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		c, cok := readCandidate(path)
		if !cok {
			return nil
		}
		stem := strings.TrimSuffix(d.Name(), ".md")
		if c.SessionID == ref || stem == ref || strings.HasPrefix(c.SessionID, ref) {
			if !ok || c.Harvested > walkTS {
				found, abs, ok, walkTS = c, path, true, c.Harvested
			}
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return Candidate{}, "", false, err
	}
	return found, abs, ok, nil
}

// SetStatus rewrites a candidate note with a new status, vault-first. Used to
// mark a candidate rejected so it is not offered again.
func SetStatus(abs string, c Candidate, status string) error {
	c.Status = status
	return vault.WriteAtomic(abs, []byte(c.Markdown()))
}
