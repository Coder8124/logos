package mcpserver

// Which project an agent is working on, decided without asking it.
//
// The problem this solves: `remember` and `recall` used to have no notion of a
// project at all, so every fact an agent stored landed in one global pile and
// every recall searched that pile. Two repositories open in two windows wrote
// into each other's memory, and the only defence was the agent volunteering a
// project name on the tools that happened to accept one.
//
// A coding agent's cwd is the repository it is working in — the assumption the
// whole product already makes, and the one internal/session relies on to read
// git state. An MCP server inherits that cwd from the host that launched it, so
// the folder is available without a protocol round trip and without the model
// cooperating. That matters: anything requiring the model to pass the right
// string is a rule it can forget, and forgetting it silently merges two
// projects rather than failing loudly.
//
// Resolution order, first non-empty wins:
//
//  1. the tool call's own `project` argument — an explicit request wins on
//     which project, though not on which worktree inside it; see below
//  2. LOGOS_PROJECT — for a host that runs the server somewhere unrelated to
//     the work, or a user who wants two folders sharing one project
//  3. the MCP roots the client advertised at initialize, when it sent any
//  4. a .logos-project marker at or above the working directory
//  5. the basename of the working directory
//
// Steps 3 and 4 interleave rather than stack: a root and a cwd are each turned
// into a name by internal/scope, which reads the marker before falling back
// to the basename. So a repository that names itself is named that way whether
// the host advertised roots or not — the marker cannot be right for one host
// and ignored by another, which is the whole reason it is a committed file.
//
// Empty is a legitimate answer, and it means global. A memory with no project
// applies everywhere, which is the pre-existing semantics in internal/memory
// and the reason recall returns a project's own memories *plus* the globals.
//
// A linked git worktree is a second axis, and it narrows continuity only.
//
// `git worktree add` does not create a second project; it creates a second
// working tree over the same repository — same objects, same history, and so
// the same accumulated knowledge. What it does create is a second HEAD, a
// second index and a second set of uncommitted files, which is exactly the half
// of continuity that must not be shared. So the two halves split the way git
// itself splits them:
//
//	memory                    → the repository. Facts about the codebase are
//	                            facts about the codebase, and a worktree that
//	                            could not recall them would be a different
//	                            project, which is not what was created.
//	sessions and checkpoints  → the worktree. "Where I stopped" is a statement
//	                            about a working tree, and two of them stopped in
//	                            different places.
//
// That makes a worktree a sub-scope rather than a scope of its own:
// sessions/kestrel/feature-x/ inside sessions/kestrel/, not beside it. A repo
// with no linked worktrees never sees any of this — the name is unchanged and
// the folder is unchanged — which is the requirement a scoping change of this
// kind lives or dies by. LOGOS_WORKTREE turns it off, mirroring LOGOS_PROJECT.

import (
	"encoding/json"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Coder8124/logos/internal/gitstate"
	"github.com/Coder8124/logos/internal/scope"
)

// resolveProject picks the project for one tool call. arg is the tool's own
// project argument, which outranks everything because it is the caller being
// explicit.
func (s *Session) resolveProject(arg string) string {
	if p := normalizeProjectArg(arg); p != "" {
		return p
	}
	return s.sessionProject()
}

// normalizeProjectArg reduces a project argument that is really a filesystem
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
// The rule is projectFromCwd's, which already argues it: a project is a name a
// human would recognise and type, and "/Users/x/code/kestrel" and
// "/Users/x/kestrel" are the same work moved, not two projects.
//
// A single separator is left alone, because that is the qualified scope
// resolveContinuity documents — "kestrel/feature-a", a linked worktree. Two or
// more, or a leading /, ~, ./ or ../, is a path and nothing else.
func normalizeProjectArg(arg string) string {
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
	if base := projectFromPath(p); base != "" {
		return base
	}
	return p
}

// sessionProject is the project this whole MCP session defaults to — the
// folder the host was launched in, unless something more specific was set.
// Computed once: the working directory cannot change under a served process,
// and re-deriving it per call would let a stray chdir silently re-scope a
// session halfway through. Only the host saying its roots changed resolves it
// again; see setRoots.
func (s *Session) sessionProject() string {
	s.projectOnce.Do(func() {
		s.project = firstNonEmpty(
			strings.TrimSpace(os.Getenv("LOGOS_PROJECT")),
			projectFromRoots(s.roots),
			projectFromCwd(),
		)
	})
	return s.project
}

// resolveContinuity picks where a session, a note or a checkpoint is filed: the
// project the work belongs to, and the linked worktree it is happening in.
// worktree is empty in a main checkout, which is every repository that has none.
//
// The worktree narrows an explicitly-given project too, unlike everything else
// here. That looks like a break with rule 1 above, and is not one: the two
// answer different questions. The project argument answers "which work is
// this", and the model
// answering it has no way of knowing which of two identical trees it is
// standing in — resume, note_progress, checkpoint and handoff all *require* a
// project, so honouring it as a complete scope would leave worktree scoping
// switched off in exactly the case it exists for. Which tree is a fact about
// the machine, and this package's whole premise is that those are observed
// rather than asked for.
//
// An argument that already contains a separator is taken as a scope the caller
// has qualified itself — "kestrel/feature-a", or a file path handed to context
// — and is left exactly as given.
func (s *Session) resolveContinuity(arg string) (project, worktree string) {
	project = s.resolveProject(arg)
	if strings.Contains(strings.TrimSpace(arg), "/") {
		return project, ""
	}
	return project, s.sessionWorktree()
}

// resolveScope is resolveContinuity as the single name the session store and
// the vault key on.
func (s *Session) resolveScope(arg string) string {
	return scopeName(s.resolveContinuity(arg))
}

// scopeName joins a project and a worktree with a path separator, because that
// is what it becomes in the vault: sessions/<project>/<worktree>/<id>.md, a
// folder inside the project's rather than a sibling of it. See
// contextpack.Pack.Continuity, which has to spell the same name.
func scopeName(project, worktree string) string {
	if project == "" || worktree == "" {
		return project
	}
	return project + "/" + worktree
}

// sessionWorktree names the linked worktree this MCP session is standing in,
// and "" when it is a main checkout, not a repository, or has no git at all.
//
// Resolved once, for the same reason the project is: the directory cannot
// change under a served process, and a stray chdir must not re-scope a session
// halfway through. Once is also what keeps this cheap — it shells out to git,
// and every tool call would otherwise pay for it.
//
// LOGOS_WORKTREE overrides the answer, and setting it to nothing is how a user
// turns worktree scoping off: two trees they want to treat as one piece of work
// have no other way to say so, because nothing else here takes the model's word
// for which tree it is in.
func (s *Session) sessionWorktree() string {
	s.worktreeOnce.Do(func() {
		if name, ok := os.LookupEnv("LOGOS_WORKTREE"); ok {
			s.worktree = strings.TrimSpace(name)
			return
		}
		s.worktree = gitstate.WorktreeName(scopeDir(s.roots))
	})
	return s.worktree
}

// repoDir is the directory this session stands in when project is the one it
// is named, and "" otherwise, so a pack only checks which repository a
// checkpoint came from when the caller means the one it is in. See
// contextpack.Request.Dir.
func (s *Session) repoDir(project string) string {
	dir := scopeDir(s.roots)
	if dir == "" || projectFromPath(dir) != project {
		return ""
	}
	return dir
}

// scopeDir is the directory the scope is read from: the first root the client
// advertised, and otherwise the working directory. The same order
// sessionProject uses, so the project and the worktree can never be read from
// two different places.
//
// LOGOS_PROJECT has no say here. It renames the work; it does not move the
// agent, and two worktrees sharing a project name by that route are precisely
// the collision worth keeping apart.
func scopeDir(roots []string) string {
	for _, r := range roots {
		if p := strings.TrimSpace(pathFromURI(r)); p != "" {
			return p
		}
	}
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	return dir
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// projectFromCwd names the project after the directory the agent is standing
// in. The basename rather than the full path: a project is a name a human
// would recognise and type at the CLI, and "/Users/x/code/kestrel" and
// "/Users/x/kestrel" are the same work moved, not two projects.
func projectFromCwd() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	return projectFromPath(dir)
}

func projectFromPath(dir string) string {
	return scope.Name(dir)
}

// projectFromRoots reads the first filesystem root the client advertised.
// Roots are how MCP says "this is what the user has open", so when a host
// sends them they are better evidence than cwd — a host may serve several
// windows from one process launched somewhere else entirely.
func projectFromRoots(roots []string) string {
	for _, r := range roots {
		if p := projectFromPath(pathFromURI(r)); p != "" {
			return p
		}
	}
	return ""
}

// pathFromURI accepts either a file:// URI or a bare path, because hosts send
// both and a root that will not parse is not worth dropping the scope over.
func pathFromURI(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.HasPrefix(raw, "file://") {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if p, err := url.PathUnescape(u.Path); err == nil {
		return p
	}
	return u.Path
}

// rootsFromInitialize pulls filesystem roots out of an initialize request.
// The MCP roots capability is normally a server→client request, which Serve
// sends once the handshake completes (see askRoots); but hosts that know their
// roots up front include them in the initialize params, and reading those costs
// nothing. When none arrive, cwd still answers.
func rootsFromInitialize(params json.RawMessage) []string {
	if len(params) == 0 {
		return nil
	}
	var p struct {
		RootURI string `json:"rootUri"`
		Roots   []struct {
			URI  string `json:"uri"`
			Path string `json:"path"`
		} `json:"roots"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil
	}
	var out []string
	if p.RootURI != "" {
		out = append(out, p.RootURI)
	}
	for _, r := range p.Roots {
		if r.URI != "" {
			out = append(out, r.URI)
		} else if r.Path != "" {
			out = append(out, r.Path)
		}
	}
	return out
}

// rootsTimeout bounds how long calls wait on a host that declared roots and
// never answered roots/list. After it the working directory answers, as it did
// before the server asked at all.
var rootsTimeout = 2 * time.Second

// clientHasRoots reports whether initialize declared the roots capability. A
// host without it has nothing to answer roots/list with, so it is never asked.
func clientHasRoots(params json.RawMessage) bool {
	var p struct {
		Capabilities struct {
			Roots json.RawMessage `json:"roots"`
		} `json:"capabilities"`
	}
	if json.Unmarshal(params, &p) != nil {
		return false
	}
	r := strings.TrimSpace(string(p.Capabilities.Roots))
	return r != "" && r != "null"
}

// rootsFromResult reads the roots out of the host's answer to roots/list. An
// error answer or an unreadable one is no roots, which leaves cwd answering.
func rootsFromResult(result json.RawMessage) []string {
	var r struct {
		Roots []struct {
			URI string `json:"uri"`
		} `json:"roots"`
	}
	if len(result) == 0 || json.Unmarshal(result, &r) != nil {
		return nil
	}
	var out []string
	for _, root := range r.Roots {
		if root.URI != "" {
			out = append(out, root.URI)
		}
	}
	return out
}

// setRoots adopts the folder the host says is open. Changed roots mean the user
// opened another folder, so the project and worktree are resolved again from
// them on the next call; unchanged or empty roots leave the session as it was,
// so a host that answers with nothing does not undo a scope already in use.
func (s *Session) setRoots(roots []string) {
	if len(roots) == 0 || slices.Equal(roots, s.roots) {
		return
	}
	s.roots = roots
	s.project, s.projectOnce = "", sync.Once{}
	s.worktree, s.worktreeOnce = "", sync.Once{}
}
