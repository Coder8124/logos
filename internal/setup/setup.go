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
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Coder8124/brain/internal/vault"
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
	// Unchanged means the host was already pointed at exactly this brain, so
	// the registration rewrote its config with the bytes already in it. Setup
	// is run more than once — after moving a vault, after an update, or just to
	// check — and calling that "updated" reports work that did not happen.
	Unchanged Outcome = "already connected"
	// Skipped means the host is not installed here.
	Skipped Outcome = "not installed"
	// Failed means it is installed and something went wrong.
	Failed Outcome = "failed"
	// Pending means the host is installed and would be wired. Only Plan produces
	// it — it is what the user is being asked to approve.
	Pending Outcome = "would wire"
)

// A Result is one host's line in the report.
type Result struct {
	Host    string
	Outcome Outcome
	Where   string // config path or command, for the report
	// Backup is the copy of this host's config taken before it was changed,
	// empty when there was nothing to copy. Reported, because a backup nobody
	// is told about is a backup nobody uses.
	Backup string
	Err    error
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
	// Config is the file this host's registration rewrites, so it can be
	// backed up first. Empty means the file does not exist yet or brain does
	// not know where it lives — a host registered through its own CLI still
	// rewrites a file, and knowing which one is the only way back from a wrong
	// --vault.
	Config func() string
	// Register points the host at this server.
	Register func(Server) (Outcome, error)
	// List reports every MCP server this host currently has registered, read
	// back rather than assumed. Nil means this host exposes no way to read
	// that back — not an error, just a question this host cannot answer.
	List func() ([]Registration, error)
}

// Registration is one server as a host currently reports it — the name it was
// given and the command line the host will actually run.
type Registration struct {
	Name    string
	Command string
}

// Plan reports what Install would do, without doing any of it.
//
// Registering brain with every AI tool on someone's machine is a large action
// for someone evaluating one integration, and it used to happen with no gate at
// all — not even --yes. Showing the list first costs one function and turns an
// imposition into a choice.
//
// It does not distinguish "would register" from "would update": finding that
// out means reading a config the user has not yet agreed to have touched, and
// for the CLI-based hosts it means asking their command, which is not free.
// Pending is the honest answer to "what will happen here".
func Plan(hosts []Host) []Result {
	out := make([]Result, 0, len(hosts))
	for _, h := range hosts {
		r := Result{Host: h.Name, Where: h.Where(), Outcome: Pending}
		if !h.Detect() {
			r.Outcome = Skipped
		}
		out = append(out, r)
	}
	return out
}

// Only keeps the hosts the user named, matched case-insensitively on a prefix so
// `--host claude-code`, `--host "Claude Code"` and `--host codex` all work. An
// unmatched name is returned so the caller can say so rather than silently
// wiring nothing.
func Only(hosts []Host, names []string) (kept []Host, unmatched []string) {
	if len(names) == 0 {
		return hosts, nil
	}
	norm := func(s string) string {
		return strings.ToLower(strings.NewReplacer(" ", "", "-", "", "_", "").Replace(s))
	}
	for _, want := range names {
		found := false
		for _, h := range hosts {
			if strings.HasPrefix(norm(h.Name), norm(want)) {
				kept = append(kept, h)
				found = true
				break
			}
		}
		if !found {
			unmatched = append(unmatched, want)
		}
	}
	return kept, unmatched
}

// Names lists every host brain knows how to wire, for error messages.
func Names(hosts []Host) []string {
	out := make([]string, 0, len(hosts))
	for _, h := range hosts {
		out = append(out, h.Name)
	}
	return out
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
		backup, err := backupConfig(h)
		if err != nil {
			r.Outcome, r.Err = Failed, err
			out = append(out, r)
			continue
		}
		r.Backup = backup
		outcome, err := h.Register(s)
		r.Outcome, r.Err = outcome, err
		if err != nil {
			r.Outcome = Failed
			out = append(out, r)
			continue
		}
		// Compared afterwards rather than predicted beforehand: whether a
		// registration changes anything is only knowable once the host's own
		// command or our own merge has run. A backup of a file nobody changed
		// is litter in the user's config directory, so it goes.
		if unchanged(h, backup) {
			r.Outcome = Unchanged
			if err := os.Remove(backup); err == nil {
				r.Backup = ""
			}
		}
		out = append(out, r)
	}
	return out
}

// backupConfig copies a host's config aside before anything rewrites it.
//
// This used to happen only inside mergeJSON, which meant it happened only for
// the two hosts with no CLI. The hosts with a CLI rewrite a config file too:
// `codex mcp add brain` replaces an existing brain entry outright — dropping
// its environment block with it — and reports "Added global MCP server". A
// user who ran setup with the wrong --vault had no way back.
//
// A config that does not exist yet needs no backup: Claude Desktop writes its
// file only once it has an MCP server, and the first one is often ours.
func backupConfig(h Host) (string, error) {
	if h.Config == nil {
		return "", nil
	}
	path := h.Config()
	if path == "" {
		return "", nil
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("could not read %s to back it up, so it was left alone: %w", path, err)
	}
	if err := os.WriteFile(path+".brain-backup", raw, 0o600); err != nil {
		return "", fmt.Errorf("could not back up %s, so it was left alone: %w", path, err)
	}
	return path + ".brain-backup", nil
}

// unchanged reports whether the config is byte-for-byte what the backup holds.
// A read that fails answers no: the outcome the host reported stands, because
// claiming nothing happened on the strength of a failed check is exactly the
// silent-success failure invariant 4 exists to prevent.
func unchanged(h Host, backup string) bool {
	if backup == "" || h.Config == nil {
		return false
	}
	before, err := os.ReadFile(backup)
	if err != nil {
		return false
	}
	after, err := os.ReadFile(h.Config())
	if err != nil {
		return false
	}
	return bytes.Equal(before, after)
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
	// Older versions of both CLIs refused a name that already exists rather
	// than replacing it, and said so on stdout. Codex now replaces it silently
	// instead, which is why Install backs the config up first (backupConfig).
	// Where the refusal does still happen it is not a failure to report — it is
	// the idempotent case, and the user asked for brain to be connected, which
	// it now is.
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

// MergeFile is mergeJSON, exported for `brain setup --config <path>` — a host
// whose location on disk brain has no convention for, but whose file is the
// same mcpServers-keyed JSON that Claude Desktop and Cursor already read. The
// internal hosts in Hosts() are a closed, curated list on purpose (see the
// package doc); this is the escape hatch for every MCP client that is not on
// it, so that "not one of the four we special-case" does not mean "brain
// cannot help you connect this".
func MergeFile(path string, s Server) (Outcome, error) {
	return mergeJSON(path, s)
}

// RenderConfig renders the server block a host's config needs, in the
// requested format, for printing rather than writing. It exists for the same
// reason MergeFile does: a host brain does not know how to find can still be
// wired, by hand, if the user can see what a working entry looks like. An
// empty format means json — the shape most MCP hosts actually use, and the
// one this package already writes for Claude Desktop and Cursor.
func RenderConfig(s Server, format string) (string, error) {
	switch format {
	case "", "json":
		return renderConfigJSON(s)
	case "toml":
		return renderConfigTOML(s), nil
	default:
		return "", fmt.Errorf("unknown format %q — brain knows: json, toml", format)
	}
}

func renderConfigJSON(s Server) (string, error) {
	wrapper := struct {
		Servers map[string]serverEntry `json:"mcpServers"`
	}{Servers: map[string]serverEntry{
		Name: {Command: s.Bin, Args: s.Args, Env: s.Env},
	}}
	out, err := json.MarshalIndent(wrapper, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out) + "\n", nil
}

// renderConfigTOML mirrors the shape Codex's own config.toml uses
// ([mcp_servers.<name>], with env as a nested table) — the one host in this
// package's list that speaks TOML rather than JSON, and so the format most
// likely to make a hand-edited entry actually match what its neighbors expect.
//
// Escaping only backslash and quote, not the full TOML basic-string grammar:
// what lands here is an absolute binary path and a vault directory, never
// arbitrary user text, so control characters and stray unicode are not a case
// this needs to cover.
func renderConfigTOML(s Server) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[mcp_servers.%s]\n", Name)
	fmt.Fprintf(&b, "command = %s\n", tomlString(s.Bin))
	if len(s.Args) > 0 {
		parts := make([]string, len(s.Args))
		for i, a := range s.Args {
			parts[i] = tomlString(a)
		}
		fmt.Fprintf(&b, "args = [%s]\n", strings.Join(parts, ", "))
	}
	if len(s.Env) > 0 {
		fmt.Fprintf(&b, "\n[mcp_servers.%s.env]\n", Name)
		for _, k := range sortedKeys(s.Env) {
			fmt.Fprintf(&b, "%s = %s\n", k, tomlString(s.Env[k]))
		}
	}
	return b.String()
}

func tomlString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

// sortedKeys makes the env table's line order deterministic. Map iteration
// order is not, and a config a user is meant to read (or diff, or paste into a
// bug report) should not reshuffle itself between two runs of the same
// command.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// mergeJSON writes the server into a host's JSON config without disturbing what
// is already there.
//
// The failure this guards against is not hypothetical: these files hold every
// other MCP server the user has connected, and clobbering one to add ours would
// be a worse bug than never registering at all. So a file that exists is parsed
// before it is touched, a malformed file is refused rather than replaced, and a
// backup is written before the new content goes down (by Install, which does
// that for every host now — see backupConfig).
func mergeJSON(path string, s Server) (Outcome, error) {
	cfg := mcpConfig{}

	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
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

	if err := vault.WriteAtomic(path, append(out, '\n')); err != nil {
		return Failed, err
	}
	if had {
		return Updated, nil
	}
	return Registered, nil
}

// readMCPServers reads back what a JSON-config host (Claude Desktop, Cursor)
// currently has registered, in the same mcpServers shape mergeJSON writes. A
// missing or empty file is not an error — nothing registered is a valid
// answer — but malformed JSON is, since a caller asking "what's here" should
// not be told "nothing" about a file that actually holds something unreadable.
func readMCPServers(path string) ([]Registration, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil, nil
	}
	cfg := mcpConfig{}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, err
	}
	rawServers, ok := cfg["mcpServers"]
	if !ok || len(rawServers) == 0 {
		return nil, nil
	}
	servers := map[string]serverEntry{}
	if err := json.Unmarshal(rawServers, &servers); err != nil {
		return nil, err
	}
	out := make([]Registration, 0, len(servers))
	for name, s := range servers {
		out = append(out, Registration{Name: name, Command: strings.Join(append([]string{s.Command}, s.Args...), " ")})
	}
	return out, nil
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
