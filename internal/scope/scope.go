// Package scope answers one question — what is the work in this directory
// called — the same way for every caller, so that a hook, the CLI and the MCP
// server cannot disagree about it.
//
// They did disagree, and it is the failure this package exists for. The name
// was the basename of the working directory, which is right for most repos and
// wrong for every repo whose folder is not what the work is called. This one is
// checked out as logos/ and is called logos; so activity was filed under
// "logos", checkpoints under "logos", and the SessionStart hook — which looks
// for a handoff under the folder name — found nothing and printed nothing,
// session after session, with no error anywhere. Two names for one project does
// not fail loudly. It just quietly stops being continuity.
//
// The fix is a file the repository carries: .logos-project, one line, the name.
// A file rather than an environment variable because the name is a property of
// the repository, not of a shell — it has to be the same answer for a hook the
// host launched with no profile, for an agent in another editor, and for a
// person typing at a terminal, and anything living in a shell profile is none
// of those. It is committed for the same reason: the second agent on this repo
// should inherit the name rather than rediscover it.
//
// Distinct from internal/project, which is about projects the rollup has
// *detected* in the vault and the dossier it assembles for each. This package
// never opens the database; it only reads a directory. Keeping them apart keeps
// the hooks — which must run in milliseconds with no index — from depending on
// anything that needs one.
package scope

import (
	"os"
	"path/filepath"
	"strings"
)

// MarkerFile is the per-repository name override. One line of text; blank lines
// and # comments are skipped, so the file has room for a sentence saying why it
// is there without that sentence becoming the project name.
const MarkerFile = ".logos-project"

// maxWalk bounds the search up the tree. A marker is meant to sit at a
// repository root, which is a handful of levels above any file in it; walking
// all the way to / instead would let a stray marker in a home directory
// silently rename every project underneath it.
const maxWalk = 24

// Name is the project for dir: the nearest .logos-project at or above it, then
// the git repository dir is inside, and otherwise dir's own basename.
//
// Callers that also honour an explicit argument or LOGOS_PROJECT check those
// first — this is deliberately only the part that reads the disk, so the
// precedence order stays written down in one place
// (internal/mcpserver/scope.go) rather than half here and half there.
func Name(dir string) string {
	if n := FromMarker(dir); n != "" {
		return n
	}
	if root := gitRoot(dir); root != "" {
		return Basename(root)
	}
	return Basename(dir)
}

// gitRoot is the nearest directory at or above dir holding a .git — a
// directory, or the file a worktree or submodule has instead — and "" when
// there is none. A host launched in internal/db filed its checkpoint under "db",
// and the session opened at the repository root never saw it.
//
// The walk stops short of the home directory: a home kept under git for its
// dotfiles is not a project, and treating it as one would file every folder
// beneath it under the user's name.
func gitRoot(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return ""
	}
	dir = filepath.Clean(dir)
	home, _ := os.UserHomeDir()
	for i := 0; i < maxWalk; i++ {
		if home != "" && dir == filepath.Clean(home) {
			return ""
		}
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

// FromMarker reads the nearest MarkerFile at or above dir, and returns "" when
// there is none, when it is unreadable, or when it holds nothing but comments.
// An unreadable marker is not an error worth failing a session over: the
// basename is a workable answer, and a hook that aborts because a file was
// briefly locked is worse than one that files a note under the folder name.
func FromMarker(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return ""
	}
	dir = filepath.Clean(dir)
	for i := 0; i < maxWalk; i++ {
		if b, err := os.ReadFile(filepath.Join(dir, MarkerFile)); err == nil {
			if n := firstMeaningfulLine(string(b)); n != "" {
				return n
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

// Basename names the project after the directory the agent is standing in. The
// basename rather than the full path: a project is a name a human would
// recognise and type at the CLI, and "/Users/x/code/kestrel" and
// "/Users/x/kestrel" are the same work moved, not two projects.
//
// Returns "" — meaning global, not "unknown" — for the places a host gets
// launched when the user has not opened a project at all. Naming a project
// after a home directory would scope every unrelated session into one bucket,
// which is worse than staying global.
func Basename(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return ""
	}
	dir = filepath.Clean(dir)
	base := filepath.Base(dir)
	// filepath.Base answers "/" with "/" and "." with "."; neither is a project.
	if base == "." || base == string(filepath.Separator) || base == "" {
		return ""
	}
	if home, err := os.UserHomeDir(); err == nil && dir == filepath.Clean(home) {
		return ""
	}
	return base
}

// firstMeaningfulLine is the marker's contents reduced to a name: the first
// line that is neither blank nor a comment, trimmed, with any path separator
// replaced.
//
// Separators are replaced rather than honoured because the name becomes a
// directory under sessions/ and a "/" in it would silently nest one project
// inside another — which is what the worktree sub-scope means, and a marker
// must not be able to forge that.
func firstMeaningfulLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.ReplaceAll(line, "/", "-")
		line = strings.ReplaceAll(line, string(filepath.Separator), "-")
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

// NormalizeArg reduces a project argument that is really a filesystem
// path to the project name it names, and leaves everything else alone.
//
// A model asked "which project" answers with the directory it is standing in,
// because that is what the host handed it. That argument was taken verbatim:
// checkpoint(project: "/Users/x/IdeaProjects/brain") filed at
// sessions/users/x/ideaprojects/brain/ — four levels deep, where session.Scopes
// reads exactly two, so the checkpoint existed and no count, list, resume or
// dead-end check could ever see it again. remember stored the same string as
// the memory's project, putting a machine-specific path carrying the user's
// account name into memories/fact.md, a file this product tells people to keep
// in git — and leaving the fact unreachable by the only name a human would type.
//
// The rule is Name's, which already argues it: a project is a name a
// human would recognise and type, and "/Users/x/code/kestrel" and
// "/Users/x/kestrel" are the same work moved, not two projects.
//
// A single separator is left alone, because that is the qualified scope
// resolveContinuity documents — "kestrel/feature-a", a linked worktree. Two or
// more, or a leading /, ~, ./ or ../, is a path and nothing else.
func NormalizeArg(arg string) string {
	p := strings.TrimSpace(arg)
	if p == "" {
		return ""
	}
	looksLikePath := strings.HasPrefix(p, "/") || strings.HasPrefix(p, "~") ||
		strings.HasPrefix(p, "./") || strings.HasPrefix(p, "../") ||
		strings.Count(strings.Trim(p, "/"), "/") >= 2
	if !looksLikePath {
		return p
	}
	if base := Name(resolve(p)); base != "" {
		return base
	}
	return p
}

// resolve makes a path argument absolute before Name walks up from it. Walked
// as written, "../etc" climbs to ".." and then to "." — filepath.Dir("..") is
// "." — so the marker search ends in the caller's own directory, and a project
// pointed at a sibling was filed under the repository the caller stood in. A
// leading ~ is the user's home, which no shell expanded for a quoted argument.
func resolve(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}
