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
//  1. the tool call's own `project` argument — an explicit request always wins
//  2. BRAIN_PROJECT — for a host that runs the server somewhere unrelated to
//     the work, or a user who wants two folders sharing one project
//  3. the MCP roots the client advertised at initialize, when it sent any
//  4. the basename of the working directory
//
// Empty is a legitimate answer, and it means global. A memory with no project
// applies everywhere, which is the pre-existing semantics in internal/memory
// and the reason recall returns a project's own memories *plus* the globals.

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// resolveProject picks the project for one tool call. arg is the tool's own
// project argument, which outranks everything because it is the caller being
// explicit.
func (s *Server) resolveProject(arg string) string {
	if p := strings.TrimSpace(arg); p != "" {
		return p
	}
	return s.sessionProject()
}

// sessionProject is the project this whole MCP session defaults to — the
// folder the host was launched in, unless something more specific was set.
// Computed once: the working directory cannot change under a served process,
// and re-deriving it per call would let a stray chdir silently re-scope a
// session halfway through.
func (s *Server) sessionProject() string {
	s.projectOnce.Do(func() {
		s.project = firstNonEmpty(
			strings.TrimSpace(os.Getenv("BRAIN_PROJECT")),
			projectFromRoots(s.roots),
			projectFromCwd(),
		)
	})
	return s.project
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
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return ""
	}
	base := filepath.Base(filepath.Clean(dir))
	// Filepath.Base answers "/" with "/" and "." with "."; neither is a project.
	if base == "." || base == string(filepath.Separator) || base == "" {
		return ""
	}
	// A home directory or a filesystem root is where a host gets launched when
	// the user has not opened a project at all. Naming a project after it would
	// scope every unrelated session into one bucket called "pragun", which is
	// worse than staying global.
	if home, err := os.UserHomeDir(); err == nil && filepath.Clean(dir) == filepath.Clean(home) {
		return ""
	}
	return base
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
// The MCP roots capability is normally a server→client request, which this
// transport has no way to issue mid-handshake; but hosts that know their roots
// up front include them in the initialize params, and reading those costs
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
