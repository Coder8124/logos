// Package setup connects brain to the agents that will use it.
//
// The engine has never been the hard part of adopting this. The hard part is
// the nine steps between cloning the repo and an agent actually answering from
// your vault — install a runtime, pull models, choose a vault, index it, find
// the right config file for your host, and hand-write JSON full of absolute
// paths. Every one of those is a place to give up, and none of them is the part
// worth having.
//
// So this package does the wiring. It finds the MCP hosts installed on the
// machine and registers brain with each one, preferring the host's own
// registration command where there is one and merging its config file where
// there is not.
//
// # Why the host's CLI comes first
//
// Claude Code and Codex both ship a command for this. Using it means their
// config format stays their problem: when they change it, their command changes
// with it and brain keeps working. Hand-writing another application's config is
// a standing bet that its format will not move, and that bet is only worth
// taking when there is no alternative — which is the case for Claude Desktop
// and Cursor.
//
// Where a file does have to be written, it is read, merged, backed up and then
// replaced atomically. Someone's other MCP servers are not ours to lose.
package setup

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/pragun/brain/internal/vault"
)

// Name is what brain calls itself in a host's server list.
const Name = "brain"

// A Server is the command a host should run to reach this brain.
type Server struct {
	// Bin is the absolute path to the brain binary. Absolute because a host
	// launches it from a working directory nobody chose.
	Bin  string
	Args []string
	// Env is what the host must set. BRAIN_VAULT belongs here and must be
	// absolute for the same reason Bin is.
	Env map[string]string
}

// Outcome says what happened to one host.
type Outcome string

const (
	// Registered means the host now points at this brain.
	Registered Outcome = "registered"
	// Updated means it already had a brain entry and it was replaced.
	Updated Outcome = "updated"
	// Skipped means the host is not installed here.
	Skipped Outcome = "not installed"
	// Failed means it is installed and something went wrong.
	Failed Outcome = "failed"
)

// A Result is one host's line in the report.
type Result struct {
	Host    string
	Outcome Outcome
	Where   string // config path or command, for the report
	Err     error
}

// A Host is one application that can talk to an MCP server.
type Host struct {
	Name string
	// Detect reports whether this host is installed. Presence-based: a command
	// on PATH, or a config directory that exists. A host nobody has installed
	// is skipped, never failed — "not installed" is not an error.
	Detect func() bool
	// Where is the config path or command shown in the report.
	Where func() string
	// Register points the host at this server.
	Register func(Server) (Outcome, error)
}

// Install registers the server with every host that is present.
//
// One host failing does not stop the others: a user with a broken Cursor config
// still wants Claude wired up, and the report tells them which is which.
func Install(s Server, hosts []Host) []Result {
	out := make([]Result, 0, len(hosts))
	for _, h := range hosts {
		r := Result{Host: h.Name, Where: h.Where()}
		if !h.Detect() {
			r.Outcome = Skipped
			out = append(out, r)
			continue
		}
		outcome, err := h.Register(s)
		r.Outcome, r.Err = outcome, err
		if err != nil {
			r.Outcome = Failed
		}
		out = append(out, r)
	}
	return out
}

// --- registering through a host's own CLI ------------------------------------

// viaCLI registers through a command the host provides. args is built by the
// caller because each CLI spells the same idea differently.
func viaCLI(bin string, args []string) (Outcome, error) {
	cmd := exec.Command(bin, args...)
	outBytes, err := cmd.CombinedOutput()
	if err == nil {
		return Registered, nil
	}
	// Both CLIs refuse a name that already exists rather than replacing it.
	// That is not a failure to report — it is the idempotent case, and the user
	// asked for brain to be connected, which it now is.
	if s := strings.ToLower(string(outBytes)); strings.Contains(s, "already exists") ||
		strings.Contains(s, "already configured") {
		return Updated, nil
	}
	return Failed, fmt.Errorf("%s: %v: %s", bin, err, strings.TrimSpace(string(outBytes)))
}

// --- registering by merging a host's config file -----------------------------

// mcpConfig is the shape shared by Claude Desktop and Cursor: a top-level
// object with an mcpServers map. Everything else in the file is preserved
// verbatim through json.RawMessage, so a host that grows new keys does not lose
// them to us.
type mcpConfig map[string]json.RawMessage

type serverEntry struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// mergeJSON writes the server into a host's JSON config without disturbing what
// is already there.
//
// The failure this guards against is not hypothetical: these files hold every
// other MCP server the user has connected, and clobbering one to add ours would
// be a worse bug than never registering at all. So a file that exists is parsed
// before it is touched, a malformed file is refused rather than replaced, and a
// backup is written before the new content goes down.
func mergeJSON(path string, s Server) (Outcome, error) {
	cfg := mcpConfig{}
	existed := false

	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		existed = true
		if len(strings.TrimSpace(string(raw))) > 0 {
			if err := json.Unmarshal(raw, &cfg); err != nil {
				return Failed, fmt.Errorf(
					"%s is not valid JSON, so it was left alone; fix or move it and re-run: %w",
					path, err)
			}
		}
	case os.IsNotExist(err):
		// First MCP server on this host. Creating the file is correct.
	default:
		return Failed, err
	}

	servers := map[string]json.RawMessage{}
	if rawServers, ok := cfg["mcpServers"]; ok && len(rawServers) > 0 {
		if err := json.Unmarshal(rawServers, &servers); err != nil {
			return Failed, fmt.Errorf("%s has an mcpServers block that is not an object: %w", path, err)
		}
	}

	_, had := servers[Name]
	entry, err := json.Marshal(serverEntry{Command: s.Bin, Args: s.Args, Env: s.Env})
	if err != nil {
		return Failed, err
	}
	servers[Name] = entry

	encoded, err := json.Marshal(servers)
	if err != nil {
		return Failed, err
	}
	cfg["mcpServers"] = encoded

	// Indented, because a person opens these files.
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return Failed, err
	}

	if existed {
		if err := os.WriteFile(path+".brain-backup", raw, 0o600); err != nil {
			return Failed, fmt.Errorf("could not back up %s, so it was left alone: %w", path, err)
		}
	}
	if err := vault.WriteAtomic(path, append(out, '\n')); err != nil {
		return Failed, err
	}
	if had {
		return Updated, nil
	}
	return Registered, nil
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func home() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return h
}

func inHome(parts ...string) string {
	h := home()
	if h == "" {
		return ""
	}
	return filepath.Join(append([]string{h}, parts...)...)
}

func parent(path string) string { return filepath.Dir(path) }

func joinPath(parts ...string) string { return filepath.Join(parts...) }

// appData is Windows' per-user config root.
func appData() string { return os.Getenv("APPDATA") }
