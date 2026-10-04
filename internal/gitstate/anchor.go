package gitstate

import (
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
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
	for _, name := range namedFiles(text) {
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

// pathspec matches the name literally where it has a directory, and anywhere
// in the tree where it is a bare file name. Literal because the name is vault
// text: a leading colon would otherwise be read as pathspec magic.
func pathspec(name string) string {
	if strings.Contains(name, "/") {
		return ":(literal)" + name
	}
	return ":(glob)**/" + name
}

// namedFiles picks out what in text reads as a repository file: a relative
// path with a directory, or a bare name with an extension. Paths come first,
// because "internal/parse/reader.go" means one file and "reader.go" may mean
// several.
func namedFiles(text string) []string {
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
