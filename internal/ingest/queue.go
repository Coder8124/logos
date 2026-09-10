package ingest

import (
	"fmt"
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

	if existing, err := readCandidate(abs); err == nil {
		if existing.Hash == c.Hash {
			return Result{Path: rel, Written: false, Reason: "already ingested"}, nil
		}
		// Same session id, changed transcript. Land beside it.
		rel = c.variantRelPath()
		abs = filepath.Join(vaultDir, filepath.FromSlash(rel))
		if existing2, err := readCandidate(abs); err == nil && existing2.Hash == c.Hash {
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

// readCandidate reads one candidate note. A file that cannot be read or has no
// recoverable session id returns an error, never a zero Candidate that looks
// absent: "unreadable" and "not there" are different facts, and collapsing them
// dropped candidates from the queue with no count and no message (invariants 3
// and 4). This is the package's (T, error) convention, not (T, bool).
func readCandidate(abs string) (Candidate, error) {
	raw, err := os.ReadFile(abs)
	if err != nil {
		return Candidate{}, err
	}
	c := Parse(string(raw), filepath.Base(abs))
	if c.SessionID == "" {
		return Candidate{}, fmt.Errorf("%s: no session id in frontmatter or filename", abs)
	}
	return c, nil
}

// Pending lists every candidate note in the vault whose status is still
// pending, newest harvest first. A promoted or rejected candidate is kept on
// disk (the record of what was decided) but never offered again.
// The second return carries one line per candidate file that could not be read
// (permission denied, no recoverable session id). It is reported with a count,
// never swallowed (invariants 3 and 4).
func Pending(vaultDir string) ([]Candidate, []string) {
	all, skipped := All(vaultDir)
	out := all[:0]
	for _, c := range all {
		if c.Status == StatusPending {
			out = append(out, c)
		}
	}
	return out, skipped
}

// All lists every candidate note regardless of status, newest first. The second
// return is the list of files that could not be read, each as "<path>: <why>".
func All(vaultDir string) ([]Candidate, []string) {
	root := filepath.Join(vaultDir, Dir)
	var out []Candidate
	var skipped []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // an unreadable subdirectory is skipped, not fatal
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		c, cerr := readCandidate(path)
		if cerr != nil {
			skipped = append(skipped, cerr.Error())
			return nil
		}
		out = append(out, c)
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		skipped = append(skipped, fmt.Sprintf("%s: %v", root, err))
	}
	sortCandidatesNewestFirst(out)
	return out, skipped
}

// sortCandidatesNewestFirst orders candidates newest harvest first. Harvested
// can be 0 (a harvest written before the field existed, or an unreadable
// frontmatter), and comparing 0 to 0 left the order down to walk order, so the
// same queue printed in a different order run to run. Started and then the
// stable filename break the tie deterministically.
func sortCandidatesNewestFirst(cs []Candidate) {
	sort.SliceStable(cs, func(i, j int) bool {
		a, b := cs[i], cs[j]
		if a.Harvested != b.Harvested {
			return a.Harvested > b.Harvested
		}
		if a.Started != b.Started {
			return a.Started > b.Started
		}
		return a.Filename() < b.Filename()
	})
}

// Find returns the pending candidate whose session id (or filename stem) matches
// ref. Used by `brain ingest review --promote <ref>`.
func Find(vaultDir, ref string) (Candidate, string, bool, error) {
	root := filepath.Join(vaultDir, Dir)
	var (
		found   Candidate
		abs     string
		ok      bool
		exact   bool
		walkTS  int64
		exactCP Candidate
		exactAb string
	)
	// An exact id or stem match is unambiguous. A prefix match is not: two
	// candidates can share a prefix, and silently picking the newest promoted
	// the wrong session with no signal. Collect every prefix match and make the
	// caller disambiguate.
	var prefixIDs []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, werr error) error {
		if werr != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		c, cerr := readCandidate(path)
		if cerr != nil {
			return nil
		}
		stem := strings.TrimSuffix(d.Name(), ".md")
		if c.SessionID == ref || stem == ref {
			// An exact match ends the search now. Walking on would let a later
			// file whose stem equals this session's id overwrite it — the same
			// silent last-wins the prefix path below was fixed to avoid.
			exact, exactCP, exactAb = true, c, path
			return filepath.SkipAll
		}
		if strings.HasPrefix(c.SessionID, ref) {
			prefixIDs = append(prefixIDs, c.SessionID)
			if !ok || c.Harvested > walkTS {
				found, abs, ok, walkTS = c, path, true, c.Harvested
			}
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return Candidate{}, "", false, err
	}
	if exact {
		return exactCP, exactAb, true, nil
	}
	if len(prefixIDs) > 1 {
		sort.Strings(prefixIDs)
		return Candidate{}, "", false, fmt.Errorf("%q matches %d candidates: %s — use a full session id", ref, len(prefixIDs), strings.Join(prefixIDs, ", "))
	}
	return found, abs, ok, nil
}

// SetStatus rewrites a candidate note with a new status, vault-first. Used to
// mark a candidate rejected so it is not offered again.
func SetStatus(abs string, c Candidate, status string) error {
	c.Status = status
	return vault.WriteAtomic(abs, []byte(c.Markdown()))
}
