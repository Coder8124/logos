// Package bootstrap seeds a cold vault from the git history already on disk.
//
// The problem it solves is the first session on an existing repository. Memory
// starts empty, so the first agent gets nothing — not because nothing is known
// about the project, but because nothing has been written down *here* yet. The
// repository has been recording the same kind of thing for months: who works on
// it, where the work concentrates, how changes are described. That is signal
// the product otherwise waits weeks to accumulate.
//
// The hard constraint is that everything here must be something git *attests*,
// not something a model infers from it. A commit message says what its author
// claimed they did; it does not say why, and it does not say whether the
// approach survived. So this package computes and never summarises: counts,
// ratios, dates, names. There is no model call in this file and there should
// never be one — a bootstrap that invented plausible project history would poison
// the vault with exactly the confident-and-wrong memories the rest of the system
// works to keep out.
//
// Everything produced is marked Source "git-history" and given a confidence
// below anything a human stated by hand, because a derived fact about a
// repository is weaker evidence than a person saying it. That also makes the
// whole seeding reversible: one DELETE on the source.
package bootstrap

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Coder8124/brain/internal/memory"
)

// Source is stamped on every memory this package produces. It is the undo:
// deleting by source removes the whole bootstrap and nothing a human wrote.
const Source = "git-history"

// gitTimeout bounds every call. A large repository on a slow disk must not
// leave `brain bootstrap` hanging with no output.
const gitTimeout = 20 * time.Second

// Confidences. All are below the 0.9 a hand-stated fact carries, and they rank
// against each other by how directly git attests the claim: a commit count is
// arithmetic, a naming convention is a majority vote over strings.
const (
	confCounted    = 0.8  // arithmetic over the log — authorship, churn, dates
	confConvention = 0.65 // a majority pattern, which a repository may still break
)

// Candidate is one memory bootstrap proposes, with the evidence that produced
// it. Evidence is kept separate from Text so a caller can show its work before
// writing anything — see cmd/brain, which prints it and asks.
type Candidate struct {
	Text       string
	Kind       memory.Kind
	Confidence float64
	Salience   float64
	// Evidence is the git output the claim was computed from, one short line.
	// Not stored in the memory; shown to the user deciding whether to accept.
	Evidence string
}

// FromGitHistory reads dir's history and returns what it can honestly say about
// the project. An empty slice is a legitimate answer: a repository with three
// commits has nothing worth seeding, and inventing something for it would be
// worse than starting cold.
func FromGitHistory(dir string, months int) []Candidate {
	if months <= 0 {
		months = 12
	}
	if !isRepo(dir) {
		return nil
	}
	since := fmt.Sprintf("--since=%d months ago", months)

	// Total commits in the window bounds everything else. Below a floor there is
	// no majority to find and no hot spot that is not noise — a repository with
	// nine commits has one author and one busy file by accident.
	//
	// Merges are excluded here because every other count below excludes them.
	// Counting them only in the denominator would report "132 of the last 152"
	// for an author who in fact wrote 132 of 132 — a ratio understated by
	// exactly the merges nobody typed.
	total := countLines(gitLines(dir, "log", since, "--no-merges", "--format=%H"))
	if total < minCommits {
		return nil
	}

	var out []Candidate
	out = append(out, authors(dir, since, total)...)
	out = append(out, hotspots(dir, since, total)...)
	if c, ok := commitStyle(dir, since, total); ok {
		out = append(out, c)
	}
	if c, ok := cadence(dir, since, total, months); ok {
		out = append(out, c)
	}
	return out
}

// minCommits is the floor below which history says nothing generalisable.
// Twenty is roughly where a majority convention stops being one person's last
// afternoon.
const minCommits = 20

// authors names who actually writes this code, and only while that is a fact
// rather than a tie. A repository where the top author holds 12% of commits has
// no "main author", and saying it does would send an agent to the wrong person.
func authors(dir, since string, total int) []Candidate {
	// HEAD explicitly, and not --all. Two reasons, both learned the hard way:
	// --all counts every ref including branches never merged, while the total
	// this is a share of comes from HEAD — mixing them prints "132 of the last
	// 129 commits" and destroys trust in every other number on the page. And
	// shortlog with no revision at all reads *stdin*, so dropping --all without
	// naming HEAD silently yields nothing.
	lines := strings.Split(gitLines(dir, "shortlog", "-sn", "--no-merges", "HEAD", since), "\n")
	type author struct {
		name string
		n    int
	}
	var as []author
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		n, name, ok := splitCount(ln)
		if !ok {
			continue
		}
		as = append(as, author{name, n})
	}
	if len(as) == 0 {
		return nil
	}
	sort.SliceStable(as, func(i, j int) bool { return as[i].n > as[j].n })

	var out []Candidate
	share := float64(as[0].n) / float64(total)
	switch {
	case share >= 0.6:
		out = append(out, Candidate{
			Text:       fmt.Sprintf("%s writes most of this codebase — %d of the last %d commits.", as[0].name, as[0].n, total),
			Kind:       memory.Person,
			Confidence: confCounted,
			Salience:   0.6,
			Evidence:   fmt.Sprintf("git shortlog -sn: %s %d/%d", as[0].name, as[0].n, total),
		})
	case len(as) >= 2:
		// No majority author is itself worth knowing: it means asking "who owns
		// this" has more than one answer, and an agent should not assume one.
		names := make([]string, 0, 3)
		for _, a := range as[:min(3, len(as))] {
			names = append(names, a.name)
		}
		out = append(out, Candidate{
			Text:       fmt.Sprintf("This codebase has no single main author; recent work is shared between %s.", englishList(names)),
			Kind:       memory.Person,
			Confidence: confCounted,
			Salience:   0.5,
			Evidence:   fmt.Sprintf("git shortlog -sn: top author holds %.0f%% of %d commits", share*100, total),
		})
	}
	return out
}

// hotspots names the directories change concentrates in. This is the item that
// most earns its place: "where does work happen here" is the question a new
// agent asks first, and the answer is measurable rather than guessable.
func hotspots(dir, since string, total int) []Candidate {
	raw := gitLines(dir, "log", since, "--no-merges", "--name-only", "--format=")
	counts := map[string]int{}
	for _, f := range strings.Split(raw, "\n") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		// Group by the top two path segments. A single segment is too coarse to
		// be useful in a repository that is mostly one directory; the full path
		// is too fine, and would report a busy file rather than a busy area.
		counts[topDirs(f, 2)]++
	}
	if len(counts) == 0 {
		return nil
	}
	type area struct {
		path string
		n    int
	}
	var as []area
	var touched int
	for p, n := range counts {
		as = append(as, area{p, n})
		touched += n
	}
	// Sorted by count, then by path, so the output does not reshuffle between
	// runs on ties — a memory that rewrites itself every bootstrap is noise.
	sort.Slice(as, func(i, j int) bool {
		if as[i].n != as[j].n {
			return as[i].n > as[j].n
		}
		return as[i].path < as[j].path
	})

	top := as[:min(3, len(as))]
	// One directory in a repository that only has one directory is not a hot
	// spot, it is the repository.
	if len(as) < 2 {
		return nil
	}
	parts := make([]string, 0, len(top))
	for _, a := range top {
		parts = append(parts, fmt.Sprintf("%s (%d)", a.path, a.n))
	}
	return []Candidate{{
		Text:       fmt.Sprintf("Change concentrates in %s — file-touch counts over the last %d commits.", englishList(parts), total),
		Kind:       memory.Context,
		Confidence: confCounted,
		Salience:   0.7,
		Evidence:   fmt.Sprintf("git log --name-only: %d paths touched across %d areas", touched, len(as)),
	}}
}

// commitStyle reports the convention in use, so an agent writing a commit
// matches the repository instead of its own defaults. Only reported when the
// pattern actually dominates — a repository that is 55% one style has no style,
// and telling an agent otherwise makes its commits look wrong half the time.
func commitStyle(dir, since string, total int) (Candidate, bool) {
	subjects := strings.Split(gitLines(dir, "log", since, "--no-merges", "--format=%s"), "\n")
	var conventional, imperativeCap, n int
	for _, s := range subjects {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		n++
		if isConventional(s) {
			conventional++
		}
		if r := []rune(s); len(r) > 0 && r[0] >= 'A' && r[0] <= 'Z' && !strings.HasSuffix(s, ".") {
			imperativeCap++
		}
	}
	if n < minCommits {
		return Candidate{}, false
	}
	const dominant = 0.75
	switch {
	case float64(conventional)/float64(n) >= dominant:
		return Candidate{
			Text:       fmt.Sprintf("Commit subjects here follow Conventional Commits (feat:, fix:, …) — %d of the last %d.", conventional, n),
			Kind:       memory.Context,
			Confidence: confConvention,
			Salience:   0.55,
			Evidence:   fmt.Sprintf("git log --format=%%s: %d/%d match type(scope):", conventional, n),
		}, true
	case float64(imperativeCap)/float64(n) >= dominant:
		return Candidate{
			Text:       fmt.Sprintf("Commit subjects here are capitalised sentences with no trailing period — %d of the last %d.", imperativeCap, n),
			Kind:       memory.Context,
			Confidence: confConvention,
			Salience:   0.5,
			Evidence:   fmt.Sprintf("git log --format=%%s: %d/%d capitalised, unpunctuated", imperativeCap, n),
		}, true
	}
	return Candidate{}, false
}

// cadence says how actively the project moves. It is what tells an arriving
// agent whether a six-week-old branch is stale or normal here.
func cadence(dir, since string, total, months int) (Candidate, bool) {
	last := git(dir, "log", "-1", "--format=%cs")
	if last == "" {
		return Candidate{}, false
	}
	perMonth := float64(total) / float64(months)
	return Candidate{
		Text: fmt.Sprintf("This project runs at roughly %.0f commits a month; the most recent is dated %s.",
			perMonth, last),
		Kind:       memory.Context,
		Confidence: confCounted,
		Salience:   0.4,
		Evidence:   fmt.Sprintf("%d commits over %d months, last %s", total, months, last),
	}, true
}

// isConventional matches "type:" and "type(scope):" prefixes without pulling in
// a parser. Anything stricter would reject the real-world spellings that make
// the convention worth detecting.
func isConventional(s string) bool {
	i := strings.Index(s, ":")
	if i <= 0 || i > 24 {
		return false
	}
	// The spec requires colon-*space*, and requiring it here is what stops
	// "http://example.com is down" from parsing as type "http" — one commit
	// about a URL would otherwise count as evidence of a convention nobody
	// follows. An empty remainder fails for the same reason: "feat:" is not a
	// subject.
	if !strings.HasPrefix(s[i+1:], " ") || strings.TrimSpace(s[i+1:]) == "" {
		return false
	}
	// The breaking-change marker sits outside the scope parens ("feat(api)!:"),
	// so it has to come off before the scope is matched, not after.
	head := strings.TrimSuffix(s[:i], "!")
	if j := strings.Index(head, "("); j >= 0 {
		if !strings.HasSuffix(head, ")") {
			return false
		}
		head = head[:j]
	}
	if head == "" {
		return false
	}
	for _, r := range head {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
}

// topDirs keeps the first n path segments, and names the repository root "/"
// rather than "" so a top-level file reads as a place.
func topDirs(p string, n int) string {
	p = filepath.ToSlash(p)
	segs := strings.Split(p, "/")
	if len(segs) <= 1 {
		return "(root)"
	}
	if len(segs)-1 < n {
		n = len(segs) - 1
	}
	return strings.Join(segs[:n], "/") + "/"
}

// splitCount reads a `git shortlog -sn` line: leading count, tab or spaces,
// then the name.
func splitCount(ln string) (int, string, bool) {
	i := strings.IndexFunc(ln, func(r rune) bool { return r < '0' || r > '9' })
	if i <= 0 {
		return 0, "", false
	}
	n, err := strconv.Atoi(ln[:i])
	if err != nil {
		return 0, "", false
	}
	name := strings.TrimSpace(ln[i:])
	if name == "" {
		return 0, "", false
	}
	return n, name, true
}

func englishList(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	case 2:
		return items[0] + " and " + items[1]
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

func countLines(s string) int {
	if strings.TrimSpace(s) == "" {
		return 0
	}
	return len(strings.Split(strings.TrimSpace(s), "\n"))
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func isRepo(dir string) bool {
	return git(dir, "rev-parse", "--is-inside-work-tree") == "true"
}

// git and gitLines mirror internal/gitstate: errors are swallowed because every
// caller treats "could not find out" and "there is nothing to find out" the
// same, and a bootstrap that reported git's exit codes would be reporting on
// repositories it was never going to seed anyway.
func git(dir string, args ...string) string {
	return strings.TrimSpace(gitLines(dir, args...))
}

func gitLines(dir string, args ...string) string {
	full := append([]string{"-C", dir}, args...)
	cmd := exec.Command("git", full...)
	done := make(chan struct{})
	var out []byte
	var err error
	go func() {
		out, err = cmd.Output()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(gitTimeout):
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		return ""
	}
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(out), "\n")
}
