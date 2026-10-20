package gitstate

import (
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// A ruling is only as current as the code it was about. "Streaming fails
// because reader.go loads the whole file" was true at the commit it was
// written against; three commits to reader.go later it may be the one thing
// standing in the way of the fix that now works. Wall-clock age cannot tell
// those apart — a year-old ruling about a file nobody touched still holds, and
// a day-old one about a file rewritten this morning may not. The commit a
// checkpoint recorded can.

// Drift is how far the files a ruling named have moved since it was recorded.
type Drift struct {
	// Commit is where the ruling was recorded.
	Commit string `json:"commit"`
	// Paths are the files it named that changed since, as it named them.
	Paths []string `json:"paths"`
	// Commits is how many commits since touched any of them.
	Commits int `json:"commits"`
}

// Note is the drift as one clause for a reader. It says "may", because a
// change to the file is evidence the cause moved, not proof: the commit could
// have been a rename of something else in it.
func (d Drift) Note() string {
	return fmt.Sprintf("recorded at %s; %s changed in %d commit%s since, so it may no longer hold",
		d.Commit, strings.Join(d.Paths, ", "), d.Commits, plural(d.Commits))
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// Anchors answers Since for one repository, remembering what it already asked
// git: a resume reads every ruling in a project's history, and many of them
// name the same file at the same commit.
type Anchors struct {
	dir  string
	memo map[string]int
}

// NewAnchors reads drift against the repository at dir. Nil when dir is not a
// repository, and a nil Anchors finds nothing, so callers need not check.
func NewAnchors(dir string) *Anchors {
	if dir == "" || !isRepo(dir) {
		return nil
	}
	return &Anchors{dir: dir, memo: map[string]int{}}
}

// maxNamed bounds the files read out of one ruling. Each costs a git call,
// and a ruling naming more than this is a list, not a cause.
const maxNamed = 5

var (
	// A recorded sha is vault text, which anyone sharing the vault wrote. Hex
	// only, so it can never reach git as an option or a revision expression.
	shaPattern = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
	// A bare file name: a name, a dot, an extension that starts with a letter,
	// so "v1.2" and "3.5GB" are not files.
	fileName = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*\.[A-Za-z][A-Za-z0-9]{0,7}$`)
	// A line suffix, as in reader.go:42, is not part of the name.
	lineSuffix = regexp.MustCompile(`(:\d+)+$`)
)

// Since reports whether files named in text changed after commit. False when
// the text names no file, when nothing it names changed, or when commit is not
// in this repository — a ruling from another clone, or one whose commit was
// rebased away, has no history here to measure against, and saying nothing is
// truer than saying it moved.
//
// Only files the text names are measured, never the checkpoint's whole file
// list. A session touches a dozen files and rules out one thing about one of
// them; flagging every ruling whose session touched a busy file would mark
// nearly all of them, and a warning on everything is read as a warning on
// nothing.
func (a *Anchors) Since(commit, text string) (Drift, bool) {
	if a == nil || !shaPattern.MatchString(commit) {
		return Drift{}, false
	}
	var d Drift
	var specs []string
	for _, name := range NamedFiles(text) {
		spec := pathspec(name)
		n, ok := a.count(commit, spec)
		if !ok {
			return Drift{}, false
		}
		if n > 0 {
			d.Paths = append(d.Paths, name)
			specs = append(specs, spec)
		}
	}
	if len(specs) == 0 {
		return Drift{}, false
	}
	// Counted again across all of them: one commit touching two named files
	// is one change, not two.
	if len(specs) == 1 {
		d.Commits, _ = a.count(commit, specs[0])
	} else {
		d.Commits, _ = a.count(commit, specs...)
	}
	d.Commit = commit
	return d, true
}

// Changes counts the commits since commit that touched the one file name. ok
// is false when that cannot be measured — no repository, a commit this clone
// does not have — which is not the same answer as zero: "has not changed
// since" is a claim, and Since's false covers both.
func (a *Anchors) Changes(commit, name string) (n int, ok bool) {
	if a == nil || !shaPattern.MatchString(commit) || name == "" {
		return 0, false
	}
	return a.count(commit, pathspec(name))
}

func (a *Anchors) count(commit string, specs ...string) (int, bool) {
	key := commit + "\x00" + strings.Join(specs, "\x00")
	if n, ok := a.memo[key]; ok {
		return n, n >= 0
	}
	args := append([]string{"rev-list", "--count", commit + "..HEAD", "--"}, specs...)
	out, err := SafeGit(a.dir, gitTimeout, args...)
	n, convErr := strconv.Atoi(strings.TrimSpace(out))
	if err != nil || convErr != nil {
		// An unknown commit fails here, and so does a timeout. Remembered as
		// a refusal so a history full of one foreign sha asks git once.
		a.memo[key] = -1
		return 0, false
	}
	a.memo[key] = n
	return n, true
}

// A anchorAtom is one single anchor out of the fixed vocabulary amp-spec.md
// §3.6 allows: git:<path>, dep:<name>, doc:<name> or time:<duration>.
type anchorAtom struct {
	kind  string
	value string
}

// parseAnchorExpr reads a ruling's `anchor:` field into OR-of-AND clauses —
// sum of products, and nothing richer, so conformance can test the whole
// grammar rather than trust an evaluator that could in principle run any
// boolean expression. " OR " separates clauses; " AND " separates the atoms
// inside one. ok is false for an empty or malformed expression — including
// one atom with no recognised kind — and the caller's answer to that is to
// fall back to the single implicit git anchor the field predates, the same
// way an unparsable commit or uid is treated as absent rather than trusted.
func parseAnchorExpr(expr string) ([][]anchorAtom, bool) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil, false
	}
	var clauses [][]anchorAtom
	for _, rawClause := range strings.Split(expr, " OR ") {
		var atoms []anchorAtom
		for _, rawAtom := range strings.Split(rawClause, " AND ") {
			kind, value, ok := strings.Cut(strings.TrimSpace(rawAtom), ":")
			if !ok {
				return nil, false
			}
			kind = strings.ToLower(strings.TrimSpace(kind))
			value = strings.TrimSpace(value)
			if value == "" {
				return nil, false
			}
			switch kind {
			case "git", "dep", "doc", "time":
			default:
				return nil, false
			}
			atoms = append(atoms, anchorAtom{kind: kind, value: value})
		}
		if len(atoms) == 0 {
			return nil, false
		}
		clauses = append(clauses, atoms)
	}
	return clauses, true
}

// SinceAnchor is Since's sibling for a ruling carrying an explicit `anchor:`
// field: a fixed AND/OR grammar over single anchors, so "stale only when
// this file AND this dependency changed" can be said without a general
// boolean evaluator. anchorExpr empty, or one that does not parse, falls
// back to Since unchanged — today's implicit git-only anchor, mined from
// text — so a record written before this field existed, or hand-edited into
// nonsense, is measured exactly as it always was rather than silently
// treated as never stale.
//
// A single atom is a one-element AND, which this evaluates the same way
// Since does for one named file: both end up asking whether that one path's
// commit count since commit is nonzero. The grammar is not a parallel code
// path that happens to agree with the old behaviour on the easy case — it is
// the old behaviour's generalisation.
func (a *Anchors) SinceAnchor(commit, anchorExpr, text string) (Drift, bool) {
	if a == nil {
		return Drift{}, false
	}
	clauses, ok := parseAnchorExpr(anchorExpr)
	if !ok {
		return a.Since(commit, text)
	}
	if !shaPattern.MatchString(commit) {
		return Drift{}, false
	}

	var d Drift
	var staleSpecs []string
	anyTrue := false
	for _, clause := range clauses {
		var clausePaths, clauseSpecs []string
		clauseTrue := true
		for _, atom := range clause {
			path, spec, stale, known := a.resolveAtom(commit, atom)
			if !known || !stale {
				clauseTrue = false
				break
			}
			clausePaths = append(clausePaths, path)
			clauseSpecs = append(clauseSpecs, spec)
		}
		if clauseTrue {
			anyTrue = true
			d.Paths = append(d.Paths, clausePaths...)
			staleSpecs = append(staleSpecs, clauseSpecs...)
		}
	}
	if !anyTrue {
		return Drift{}, false
	}
	// Counted the same way Since counts a multi-file ruling: one commit
	// touching two anchors is one change, not two.
	if len(staleSpecs) == 1 {
		d.Commits, _ = a.count(commit, staleSpecs[0])
	} else {
		d.Commits, _ = a.count(commit, staleSpecs...)
	}
	d.Commit = commit
	return d, true
}

// resolveAtom measures one anchor. known is false when it cannot be
// measured at all — reported the same way Since's own "cannot measure"
// cases are, so an AND containing one is never falsely called stale by
// treating "unknown" as "unchanged".
//
// git:, dep: and doc: all resolve to the same question: has the named path
// had a commit since commit. There is no registry to ask whether a
// dependency's version moved and no resolver for whether a doc went stale —
// the file that names either (a manifest, a lockfile, the doc itself) is
// the one real signal this repository can give for any of them, so dep:
// and doc: are measured through it rather than left permanently unknown or
// given a resolver that would be fabricating an answer the codebase cannot
// back up. time: is the one anchor that is not a path at all: a trivial
// wall-clock comparison against when commit itself was made.
func (a *Anchors) resolveAtom(commit string, at anchorAtom) (path, spec string, stale, known bool) {
	switch at.kind {
	case "git", "dep", "doc":
		spec = pathspec(at.value)
		n, ok := a.count(commit, spec)
		if !ok {
			return "", "", false, false
		}
		return at.value, spec, n > 0, true
	case "time":
		dur, err := time.ParseDuration(at.value)
		if err != nil {
			return "", "", false, false
		}
		at2, ok := a.commitTime(commit)
		if !ok {
			return "", "", false, false
		}
		return at.value, "", time.Since(at2) > dur, true
	default:
		return "", "", false, false
	}
}

// commitTime is commit's own timestamp, memoized under the same scheme
// count uses — a resume walks a project's whole history, and a time: anchor
// on an old commit is asked about it exactly once per run this way.
func (a *Anchors) commitTime(commit string) (time.Time, bool) {
	key := "time\x00" + commit
	if n, ok := a.memo[key]; ok {
		if n < 0 {
			return time.Time{}, false
		}
		return time.Unix(int64(n), 0), true
	}
	out, err := SafeGit(a.dir, gitTimeout, "show", "-s", "--format=%ct", commit)
	ts, convErr := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if err != nil || convErr != nil {
		a.memo[key] = -1
		return time.Time{}, false
	}
	a.memo[key] = int(ts)
	return time.Unix(ts, 0), true
}

// pathspec matches the name literally where it has a directory, and anywhere
// in the tree where it is a bare file name. Literal because the name is vault
// text: a leading colon would otherwise be read as pathspec magic.
func pathspec(name string) string {
	if strings.Contains(name, "/") {
		return ":(literal)" + name
	}
	return ":(glob)**/" + name
}

// NamedFiles picks out what in text reads as a repository file: a relative
// path with a directory, or a bare name with an extension. Paths come first,
// because "internal/parse/reader.go" means one file and "reader.go" may mean
// several.
func NamedFiles(text string) []string {
	var paths, bare []string
	seen := map[string]bool{}
	for _, tok := range strings.Fields(text) {
		tok = strings.Trim(tok, "`'\"()[]{}<>,;:!?*")
		tok = strings.TrimRight(tok, ".")
		tok = lineSuffix.ReplaceAllString(tok, "")
		tok = strings.TrimPrefix(tok, "./")
		if tok == "" || seen[tok] || strings.ContainsAny(tok, "*?[]\\") {
			continue
		}
		switch {
		case strings.Contains(tok, "/"):
			// Not a URL, not absolute, not climbing out of the repository.
			if strings.Contains(tok, "://") || strings.HasPrefix(tok, "/") ||
				strings.Contains(tok, "..") || !fileName.MatchString(path.Base(tok)) {
				continue
			}
			paths = append(paths, tok)
		case fileName.MatchString(tok):
			bare = append(bare, tok)
		default:
			continue
		}
		seen[tok] = true
	}
	// A bare name already spelled out as a path is the same file.
	for _, b := range bare {
		dup := false
		for _, p := range paths {
			if path.Base(p) == b {
				dup = true
				break
			}
		}
		if !dup {
			paths = append(paths, b)
		}
	}
	out := paths
	if len(out) > maxNamed {
		out = out[:maxNamed]
	}
	return out
}
